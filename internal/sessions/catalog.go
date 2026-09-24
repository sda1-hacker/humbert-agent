package sessions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// sessionCatalog 保存会话控制面；消息和压缩检查点仍只写入 session.jsonl。
// 旧 config.json 不参与读取；数据库是会话元数据的唯一事实来源。
type sessionCatalog struct{ db *sql.DB }

func openSessionCatalog(agentsRoot string) (*sessionCatalog, error) {
	path := filepath.Join(agentsRoot, "session-metadata.sqlite")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("会话元数据库不是普通文件: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// busy_timeout 必须随 DSN 应用于连接池里的每一条连接。
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "_busy_timeout=5000"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(2)
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, title TEXT NOT NULL,
			archived INTEGER NOT NULL DEFAULT 0, cwd TEXT NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS sessions_by_agent_updated ON sessions(agent_id, updated_at DESC, id)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("初始化会话元数据库失败: %w", err)
		}
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		_ = db.Close()
		return nil, err
	}
	if version > 1 {
		_ = db.Close()
		return nil, fmt.Errorf("会话元数据库版本 %d 高于当前支持的版本", version)
	}
	if version == 0 {
		if _, err := db.Exec(`PRAGMA user_version=1`); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &sessionCatalog{db: db}, nil
}

func (c *sessionCatalog) close() error { return c.db.Close() }

func (c *sessionCatalog) get(ctx context.Context, id string) (Session, error) {
	var value Session
	var archived int
	var created, updated int64
	err := c.db.QueryRowContext(ctx, `SELECT id, agent_id, title, archived, cwd, created_at, updated_at FROM sessions WHERE id=?`, id).
		Scan(&value.ID, &value.AgentID, &value.Title, &archived, &value.CWD, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrSessionNotFound
	}
	if err != nil {
		return Session{}, err
	}
	value.Archived = archived != 0
	value.CreatedAt = time.Unix(0, created).UTC()
	value.UpdatedAt = time.Unix(0, updated).UTC()
	return value, nil
}

func (c *sessionCatalog) list(ctx context.Context, agentID string) ([]Session, error) {
	rows, err := c.db.QueryContext(ctx, `SELECT id, agent_id, title, archived, cwd, created_at, updated_at FROM sessions WHERE agent_id=? ORDER BY updated_at DESC, id`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Session{}
	for rows.Next() {
		var value Session
		var archived int
		var created, updated int64
		if err := rows.Scan(&value.ID, &value.AgentID, &value.Title, &archived, &value.CWD, &created, &updated); err != nil {
			return nil, err
		}
		value.Archived = archived != 0
		value.CreatedAt = time.Unix(0, created).UTC()
		value.UpdatedAt = time.Unix(0, updated).UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}

func (c *sessionCatalog) insert(ctx context.Context, value Session) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO sessions(id,agent_id,title,archived,cwd,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		value.ID, value.AgentID, value.Title, value.Archived, value.CWD, value.CreatedAt.UnixNano(), value.UpdatedAt.UnixNano())
	return err
}

func (c *sessionCatalog) rename(ctx context.Context, id, title string) error {
	result, err := c.db.ExecContext(ctx, `UPDATE sessions SET title=?, updated_at=max(updated_at,?) WHERE id=?`, title, time.Now().UTC().UnixNano(), id)
	return existingSessionResult(result, err)
}

func (c *sessionCatalog) setArchived(ctx context.Context, id string, archived bool) error {
	result, err := c.db.ExecContext(ctx, `UPDATE sessions SET archived=?, updated_at=max(updated_at,?) WHERE id=?`, archived, time.Now().UTC().UnixNano(), id)
	return existingSessionResult(result, err)
}

func existingSessionResult(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (c *sessionCatalog) touch(ctx context.Context, id string, updatedAt time.Time) error {
	_, err := c.db.ExecContext(ctx, `UPDATE sessions SET updated_at=max(updated_at,?) WHERE id=?`, updatedAt.UnixNano(), id)
	return err
}

func (c *sessionCatalog) delete(ctx context.Context, id string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id)
	return err
}

func (c *sessionCatalog) deleteAgent(ctx context.Context, agentID string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM sessions WHERE agent_id=?`, agentID)
	return err
}

func (c *sessionCatalog) prune(ctx context.Context, seen map[string]string) error {
	rows, err := c.db.QueryContext(ctx, `SELECT id FROM sessions`)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if _, exists := seen[id]; !exists {
			stale = append(stale, id)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range stale {
		if err := c.delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}
