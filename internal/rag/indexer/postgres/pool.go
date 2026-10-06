package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// EnsureExtensions 使用普通连接安装 pgvector 与 ParadeDB，再创建注册向量类型的连接池。
func EnsureExtensions(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("connect postgres for extension bootstrap: %w", err)
	}
	defer conn.Close(ctx)
	var serverVersion int
	if err := conn.QueryRow(ctx, `SELECT current_setting('server_version_num')::int`).Scan(&serverVersion); err != nil {
		return err
	}
	if serverVersion < 150000 {
		return fmt.Errorf("rag backend: PostgreSQL 15+ required")
	}

	for _, statement := range []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		`CREATE EXTENSION IF NOT EXISTS pg_search`,
	} {
		if _, err := conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure postgres extension: %w", err)
		}
	}

	return CheckExtensions(ctx, conn)
}

// NewPool 创建连接池，并为每条连接注册 pgvector 编解码器；数据库扩展须先安装。
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}

	if config.ConnConfig.ConnectTimeout == 0 {
		config.ConnConfig.ConnectTimeout = 10 * time.Second
	}
	if _, ok := config.ConnConfig.RuntimeParams["statement_timeout"]; !ok {
		config.ConnConfig.RuntimeParams["statement_timeout"] = "30000"
	}
	if _, ok := config.ConnConfig.RuntimeParams["lock_timeout"]; !ok {
		config.ConnConfig.RuntimeParams["lock_timeout"] = "5000"
	}
	previousAfterConnect := config.AfterConnect

	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if previousAfterConnect != nil {
			if err := previousAfterConnect(ctx, conn); err != nil {
				return err
			}
		}

		if err := CheckExtensions(ctx, conn); err != nil {
			return err
		}
		if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
			return fmt.Errorf("register pgvector types: %w", err)
		}

		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	// pgxpool 默认 lazy connect。
	//
	// Composition Root 应该在启动期发现配置问题，
	// 而不是直到第一条 Search 才发现数据库不可用。
	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return pool, nil
}
