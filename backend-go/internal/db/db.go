// Package db 管理 Postgres 连接池。
//
// 读池与写池分开配置：当前两者指向同一个库，但接口先留出来，
// 将来规模上来时只需要多给一个 APP_READ_DATABASE_URL。
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Pools struct {
	Write *pgxpool.Pool
	Read  *pgxpool.Pool
}

func Open(ctx context.Context, writeDSN, readDSN string, maxConns int32) (*Pools, error) {
	write, err := newPool(writeDSN, maxConns)
	if err != nil {
		return nil, fmt.Errorf("write pool: %w", err)
	}
	if readDSN == "" || readDSN == writeDSN {
		return &Pools{Write: write, Read: write}, nil
	}
	read, err := newPool(readDSN, maxConns)
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("read pool: %w", err)
	}
	return &Pools{Write: write, Read: read}, nil
}

func newPool(dsn string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	return pgxpool.NewWithConfig(context.Background(), cfg)
}

func (p *Pools) Close() {
	if p == nil {
		return
	}
	if p.Read != nil && p.Read != p.Write {
		p.Read.Close()
	}
	if p.Write != nil {
		p.Write.Close()
	}
}

func (p *Pools) Ping(ctx context.Context) error {
	if p == nil || p.Write == nil {
		return fmt.Errorf("no database pool")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return p.Write.Ping(ctx)
}
