package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"challenge-api/internal/auth"
	"challenge-api/internal/db"
	"challenge-api/internal/migrate"
)

// runMigrate 执行内嵌的 schema 迁移；已经是最新时不写任何东西。
func runMigrate(pools *db.Pools, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	applied, err := migrate.Run(ctx, pools.Write)
	if err != nil {
		logger.Error("迁移失败", "error", err, "已执行", applied)
		os.Exit(1)
	}
	if len(applied) == 0 {
		logger.Info("数据库已是最新，无需迁移")
		return
	}
	logger.Info("迁移完成", "版本数", len(applied), "版本", applied)
}

// runCreateAdmin 在空库上建第一个可用账号。
//
// 口令只从标准输入读，不接受命令行参数——命令行会留在 shell 历史与进程列表里。
// 用法：
//
//	echo '强口令' | docker compose ... run --rm go-api create-admin \
//	    --email you@example.com --name 你的名字 --password-stdin
func runCreateAdmin(pools *db.Pools, logger *slog.Logger) {
	fs := flag.NewFlagSet("create-admin", flag.ExitOnError)
	email := fs.String("email", "", "邮箱（必填）")
	name := fs.String("name", "", "显示名，默认取邮箱前缀")
	role := fs.String("role", "admin", "角色：admin 或 staff")
	fromStdin := fs.Bool("password-stdin", false, "从标准输入读取口令")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	mail := strings.ToLower(strings.TrimSpace(*email))
	if !strings.Contains(mail, "@") || len(mail) > 320 {
		fmt.Fprintln(os.Stderr, "邮箱不合法")
		os.Exit(2)
	}
	if *role != "admin" && *role != "staff" {
		fmt.Fprintln(os.Stderr, "角色只能是 admin 或 staff")
		os.Exit(2)
	}
	if !*fromStdin {
		fmt.Fprintln(os.Stderr, "需要用 --password-stdin 从标准输入提供口令")
		os.Exit(2)
	}
	display := strings.TrimSpace(*name)
	if display == "" {
		display = strings.SplitN(mail, "@", 2)[0]
	}
	if len([]rune(display)) > 120 {
		fmt.Fprintln(os.Stderr, "显示名不能超过 120 字")
		os.Exit(2)
	}

	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 4096))
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取口令失败：%v\n", err)
		os.Exit(1)
	}
	password := strings.TrimRight(string(raw), "\r\n")
	if len(password) < 8 || len(password) > 200 {
		fmt.Fprintln(os.Stderr, "口令长度需要在 8 到 200 个字符之间")
		os.Exit(2)
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成口令哈希失败：%v\n", err)
		os.Exit(1)
	}
	id, err := auth.NewID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "生成 ID 失败：%v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var created bool
	err = pools.Write.QueryRow(ctx, `
		INSERT INTO users (id, email, password_hash, display_name, locale, role, status,
			mode, llm_blocked, digest_comments, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'zh', $5, 'active', 'both', false, true, $6, $6)
		ON CONFLICT (email) DO NOTHING
		RETURNING true`, id, mail, hash, display, *role, time.Now().UTC()).Scan(&created)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			fmt.Fprintf(os.Stderr, "该邮箱已存在：%s\n", mail)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "建号失败：%v\n", err)
		os.Exit(1)
	}
	logger.Info("已创建账号", "email", mail, "role", *role)
}
