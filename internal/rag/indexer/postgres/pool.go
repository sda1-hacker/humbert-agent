package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

// EnsureExtensions 使用“普通 pgx connection”确保数据库扩展存在。
//
// 为什么不能直接使用我们的正式 pgxpool？
//
// 正式 Pool 的 AfterConnect 会执行：
//
//	pgxvec.RegisterTypes()
//
// RegisterTypes 会先查询：
//
//	vector
//	halfvec
//	sparsevec
//
// 的 PostgreSQL OID。
//
// 如果 vector extension 还没有安装，RegisterTypes 会直接失败。
//
// 所以全新数据库启动顺序必须是：
//
//	普通 pgx.Connect
//	    ↓
//	CREATE EXTENSION vector
//	CREATE EXTENSION pg_search
//	    ↓
//	Close
//	    ↓
//	NewPool
//	    ↓
//	RegisterTypes
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

// NewPool 创建已经注册 pgvector Codec 的正式连接池。
//
// 前提：
//
//	vector extension 已经存在。
//
// 开发环境可以：
//
//	EnsureExtensions(ctx, dsn)
//	NewPool(ctx, dsn)
//
// 生产环境通常由 DBA / Migration 系统提前安装 Extension，
// 然后直接 NewPool 即可。
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
