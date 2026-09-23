package searchindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// Index is a disposable search projection. Session JSONL remains authoritative.
type Index struct {
	db *sql.DB
	mu sync.Mutex
}

type Session struct {
	ID, AgentID, Title string
	Archived           bool
	Revision           int64
}

type Message struct {
	EntryID, Role, Timestamp, Content string
}

type Result struct {
	SessionID string `json:"sessionID"`
	AgentID   string `json:"agentID"`
	Title     string `json:"title"`
	Archived  bool   `json:"archived"`
	EntryID   string `json:"entryID"`
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	Snippet   string `json:"snippet"`
}

func Open(path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, agent_id TEXT NOT NULL, title TEXT NOT NULL, archived INTEGER NOT NULL, revision INTEGER NOT NULL)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS messages USING fts5(session_id UNINDEXED, entry_id UNINDEXED, role UNINDEXED, timestamp UNINDEXED, content, tokenize='trigram')`,
		`CREATE TABLE IF NOT EXISTS documents (agent_id TEXT NOT NULL, root TEXT NOT NULL, path TEXT NOT NULL, size INTEGER NOT NULL, modified INTEGER NOT NULL, PRIMARY KEY(agent_id,root,path))`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS document_lines USING fts5(agent_id UNINDEXED, root UNINDEXED, path UNINDEXED, line UNINDEXED, content, tokenize='trigram')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("初始化搜索索引失败: %w", err)
		}
	}
	// 文档索引是可重建投影。提取格式由纯文本升级为 Markdown 时，
	// 同样的文件大小与 mtime 不能证明旧索引仍然有效。
	var documentFormatVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&documentFormatVersion); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("读取文档索引版本失败: %w", err)
	}
	if documentFormatVersion != 1 {
		tx, err := db.Begin()
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("升级文档索引失败: %w", err)
		}
		for _, statement := range []string{`DELETE FROM document_lines`, `DELETE FROM documents`, `PRAGMA user_version=1`} {
			if _, err := tx.Exec(statement); err != nil {
				_ = tx.Rollback()
				_ = db.Close()
				return nil, fmt.Errorf("升级文档索引失败: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("提交文档索引升级失败: %w", err)
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Index{db: db}, nil
}

func (i *Index) Close() error { return i.db.Close() }

func (i *Index) CachedSession(ctx context.Context, id string) (Session, bool, error) {
	var session Session
	var archived int
	err := i.db.QueryRowContext(ctx, `SELECT agent_id,title,archived,revision FROM sessions WHERE id=?`, id).Scan(&session.AgentID, &session.Title, &archived, &session.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	session.ID, session.Archived = id, archived != 0
	return session, err == nil, err
}

// Replace writes a complete active-branch snapshot in one transaction.
func (i *Index) Replace(ctx context.Context, session Session, messages []Message) error {
	return i.ReplaceStream(ctx, session, func(add func(Message) error) error {
		for _, message := range messages {
			if err := add(message); err != nil {
				return err
			}
		}
		return nil
	})
}

// ReplaceStream indexes one message at a time so a long conversation does not
// require another in-memory copy of its entire active branch.
func (i *Index) ReplaceStream(ctx context.Context, session Session, visit func(add func(Message) error) error) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM messages WHERE session_id=?`, session.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO sessions(id,agent_id,title,archived,revision) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET agent_id=excluded.agent_id,title=excluded.title,archived=excluded.archived,revision=excluded.revision`, session.ID, session.AgentID, session.Title, session.Archived, session.Revision); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages(session_id,entry_id,role,timestamp,content) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	if err := visit(func(message Message) error {
		if strings.TrimSpace(message.Content) == "" {
			return nil
		}
		if _, err := stmt.ExecContext(ctx, session.ID, message.EntryID, message.Role, message.Timestamp, message.Content); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func (i *Index) UpdateSessionMetadata(ctx context.Context, session Session) error {
	_, err := i.db.ExecContext(ctx, `UPDATE sessions SET agent_id=?,title=?,archived=? WHERE id=?`, session.AgentID, session.Title, session.Archived, session.ID)
	return err
}

func (i *Index) Prune(ctx context.Context, keep map[string]bool) error {
	rows, err := i.db.QueryContext(ctx, `SELECT id FROM sessions`)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if !keep[id] {
			stale = append(stale, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range stale {
		if _, err := i.db.ExecContext(ctx, `DELETE FROM messages WHERE session_id=?`, id); err != nil {
			return err
		}
		if _, err := i.db.ExecContext(ctx, `DELETE FROM sessions WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

func (i *Index) Search(ctx context.Context, query string, limit int) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []Result{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	var rows *sql.Rows
	var err error
	if utf8.RuneCountInString(query) >= 3 {
		literal := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		rows, err = i.db.QueryContext(ctx, `SELECT m.session_id,s.agent_id,s.title,s.archived,m.entry_id,m.role,m.timestamp,snippet(messages,4,'','', '…',20) FROM messages m JOIN sessions s ON s.id=m.session_id WHERE messages MATCH ? ORDER BY m.timestamp DESC LIMIT ?`, literal, limit)
	} else {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
		rows, err = i.db.QueryContext(ctx, `SELECT m.session_id,s.agent_id,s.title,s.archived,m.entry_id,m.role,m.timestamp,substr(m.content,1,300) FROM messages m JOIN sessions s ON s.id=m.session_id WHERE m.content LIKE ? ESCAPE '\' ORDER BY m.timestamp DESC LIMIT ?`, `%`+escaped+`%`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]Result, 0)
	for rows.Next() {
		var result Result
		if err := rows.Scan(&result.SessionID, &result.AgentID, &result.Title, &result.Archived, &result.EntryID, &result.Role, &result.Timestamp, &result.Snippet); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}
