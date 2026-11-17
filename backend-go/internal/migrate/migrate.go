// Package migrate 在 PostgreSQL 上执行 schema 迁移。
//
// 迁移文件来自 `sql/`，按文件名顺序执行（文件名带零填充序号）。schema 的唯一
// 来源曾经是 backend/migrations 下的 Alembic 版本；`scripts/export_schema_sql.py`
// 把它们编译成这里的 SQL。Python 实现退场之后，新增迁移直接在这里加一个
// `NNNN_<版本>_<说明>.sql`，规则见 backend-go/README.md。
//
// 只支持 PostgreSQL：依赖事务性 DDL、advisory lock 与 timestamptz。
package migrate

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// sqlFiles 打进二进制，部署时不需要额外的挂载或工具链。
//
//go:embed sql/*.sql
var sqlFiles embed.FS

// advisoryLockKey 只用来串行化多个实例同时执行迁移，数值本身没有含义。
const advisoryLockKey int64 = 8124001

// schemaMigrations 记录已经执行过的文件；与 Alembic 的 alembic_version 并存，
// 后者由迁移文件自己维护，将来若需要用 Alembic 工具接管也能对上。
const createTrackingTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    text PRIMARY KEY,
	applied_at timestamptz NOT NULL
)`

// Versions 按执行顺序返回全部迁移文件名。
func Versions() ([]string, error) {
	entries, err := fs.ReadDir(sqlFiles, "sql")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("没有内嵌任何迁移文件")
	}
	return names, nil
}

// Run 执行尚未应用过的迁移，返回本次实际执行的版本。
//
// 每个文件在独立事务里执行：DDL 与记录要么一起生效，要么一起回滚，
// 不会出现「表建好了但没记账、下次重跑报错」的半成品状态。
func Run(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	names, err := Versions()
	if err != nil {
		return nil, err
	}

	// advisory lock 是会话级的，必须固定在同一条连接上。
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取迁移连接: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return nil, fmt.Errorf("获取迁移锁: %w", err)
	}
	defer func() {
		// 释放锁不受请求 context 影响：调用方取消时也要尽量解锁。
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	}()

	if _, err := conn.Exec(ctx, createTrackingTable); err != nil {
		return nil, fmt.Errorf("建迁移记录表: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("读取迁移记录: %w", err)
	}
	done := make(map[string]bool, len(names))
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取迁移记录: %w", err)
		}
		done[version] = true
	}
	rows.Close()
	if rows.Err() != nil {
		return nil, fmt.Errorf("读取迁移记录: %w", rows.Err())
	}

	applied := make([]string, 0, len(names))
	for _, name := range names {
		if done[name] {
			continue
		}
		body, err := sqlFiles.ReadFile("sql/" + name)
		if err != nil {
			return applied, fmt.Errorf("读取 %s: %w", name, err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, fmt.Errorf("%s: 开启事务: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())`,
			name); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("%s: 记录版本: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, fmt.Errorf("%s: 提交: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}
