package indexer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"

	ragconfig "github.com/sda1-hacker/humbert-agent/internal/rag/config"
)

// EnsureDatabase 仅供显式初始化使用：先尝试目标库，只有库不存在才通过维护库 postgres 建库。
// 不创建角色或修改已有库；首次建库需当前角色具备 CREATEDB 权限，不在事务中执行。
func EnsureDatabase(ctx context.Context, dsn string, cfg ragconfig.Postgres) error {
	config, err := parsePoolConfig(dsn, false, cfg)
	if err != nil {
		return err
	}
	name := config.ConnConfig.Database
	if name == "" || len(name) > 63 || strings.ContainsRune(name, 0) {
		return errors.New("rag: 数据库名不能为空、包含空字符或超过 63 字节")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Effective().MigrationTimeout)
	defer cancel()
	conn, err := pgx.ConnectConfig(ctx, config.ConnConfig)
	if err == nil {
		return conn.Close(ctx)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "3D000" {
		return fmt.Errorf("连接 RAG 数据库失败: %w", err)
	}
	// 复制驱动配置，仅切换数据库，保留主机、端口、凭据、TLS 及统一超时设置。
	admin := config.ConnConfig.Copy()
	admin.Database = "postgres"
	admin.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.Effective().MigrationTimeout.Milliseconds(), 10)
	conn, err = pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return fmt.Errorf("连接维护库 postgres 失败，无法创建 RAG 数据库: %w", err)
	}
	defer conn.Close(ctx)
	// 数据库名不能用 SQL 值参数；交给 pgx 转义标识符，避免拼接未转义的配置值。
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		// 其他初始化进程可能已经创建同名库；重新检查目录，兼容重复库和唯一键冲突。
		var exists bool
		if checkErr := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists); checkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("创建 RAG 数据库失败，请检查 CREATEDB 权限: %w", err)
	}
	return nil
}

// EnsureExtensions 使用普通连接安装 pgvector 与 ParadeDB，再创建注册向量类型的连接池。
func EnsureExtensions(ctx context.Context, dsn string, cfg ragconfig.Postgres) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg = cfg.Effective()
	ctx, cancel := context.WithTimeout(ctx, cfg.ExtensionTimeout)
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
	if serverVersion < 140000 {
		return fmt.Errorf("rag backend: PostgreSQL 14+ required")
	}

	for _, statement := range []string{`CREATE EXTENSION IF NOT EXISTS vector`, `CREATE EXTENSION IF NOT EXISTS pg_search`} {
		if _, err := conn.Exec(ctx, statement); err != nil {
			return fmt.Errorf("ensure postgres extension: %w", err)
		}
	}

	return CheckExtensions(ctx, conn)
}

// NewPool 创建连接池，并为每条连接注册 pgvector 编解码器；数据库扩展须先安装。
func NewPool(ctx context.Context, dsn string, cfg ragconfig.Postgres) (*pgxpool.Pool, error) {
	return newPool(ctx, dsn, true, cfg)
}

// NewDocumentPool 仅连接权威文档库，无需安装 pgvector 或 pg_search。
func NewDocumentPool(ctx context.Context, dsn string, cfg ragconfig.Postgres) (*pgxpool.Pool, error) {
	return newPool(ctx, dsn, false, cfg)
}

// parsePoolConfig 只准备驱动参数，不建立连接；DSN 显式设置优先于统一默认值。
func parsePoolConfig(dsn string, withSearch bool, cfg ragconfig.Postgres) (*pgxpool.Config, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.Effective()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}

	if config.ConnConfig.ConnectTimeout == 0 {
		config.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}
	if _, ok := config.ConnConfig.RuntimeParams["statement_timeout"]; !ok {
		config.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.StatementTimeout.Milliseconds(), 10)
	}
	if _, ok := config.ConnConfig.RuntimeParams["lock_timeout"]; !ok {
		config.ConnConfig.RuntimeParams["lock_timeout"] = strconv.FormatInt(cfg.LockTimeout.Milliseconds(), 10)
	}
	previousAfterConnect := config.AfterConnect

	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if previousAfterConnect != nil {
			if err := previousAfterConnect(ctx, conn); err != nil {
				return err
			}
		}

		if withSearch {
			if err := CheckExtensions(ctx, conn); err != nil {
				return err
			}
			if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
				return fmt.Errorf("register pgvector types: %w", err)
			}
			// 候选池上限为 200；支持迭代扫描的版本再补足集合过滤造成的召回损失。
			if _, configured := config.ConnConfig.RuntimeParams["hnsw.ef_search"]; !configured {
				if _, err := conn.Exec(ctx, `SET hnsw.ef_search=200`); err != nil {
					return err
				}
			}
			var version string
			if err := conn.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname='vector'`).Scan(&version); err != nil {
				return err
			}
			_, configured := config.ConnConfig.RuntimeParams["hnsw.iterative_scan"]
			if !configured && versionAtLeast(version, 0, 8, 0) {
				if _, err := conn.Exec(ctx, `SET hnsw.iterative_scan='strict_order'`); err != nil {
					return err
				}
			}
		}

		return nil
	}

	return config, nil
}

func newPool(ctx context.Context, dsn string, withSearch bool, cfg ragconfig.Postgres) (*pgxpool.Pool, error) {
	config, err := parsePoolConfig(dsn, withSearch, cfg)
	if err != nil {
		return nil, err
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
