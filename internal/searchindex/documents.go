package searchindex

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"
)

type DocumentResult struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

func (i *Index) DocumentCurrent(ctx context.Context, agentID, root, path string, size, modified int64) (bool, error) {
	var oldSize, oldModified int64
	err := i.db.QueryRowContext(ctx, `SELECT size,modified FROM documents WHERE agent_id=? AND root=? AND path=?`, agentID, root, path).Scan(&oldSize, &oldModified)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return oldSize == size && oldModified == modified, err
}

// ReplaceDocument atomically replaces extracted lines when a file changes.
func (i *Index) ReplaceDocument(ctx context.Context, agentID, root, path string, size, modified int64, content string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_lines WHERE agent_id=? AND root=? AND path=?`, agentID, root, path); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO documents(agent_id,root,path,size,modified) VALUES(?,?,?,?,?) ON CONFLICT(agent_id,root,path) DO UPDATE SET size=excluded.size,modified=excluded.modified`, agentID, root, path, size, modified); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO document_lines(agent_id,root,path,line,content) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for index, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, agentID, root, path, index+1, line); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (i *Index) PruneDocuments(ctx context.Context, agentID, root string, seen map[string]bool) error {
	rows, err := i.db.QueryContext(ctx, `SELECT path FROM documents WHERE agent_id=? AND root=?`, agentID, root)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return err
		}
		if !seen[path] {
			stale = append(stale, path)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, path := range stale {
		if _, err := i.db.ExecContext(ctx, `DELETE FROM document_lines WHERE agent_id=? AND root=? AND path=?`, agentID, root, path); err != nil {
			return err
		}
		if _, err := i.db.ExecContext(ctx, `DELETE FROM documents WHERE agent_id=? AND root=? AND path=?`, agentID, root, path); err != nil {
			return err
		}
	}
	return nil
}

func (i *Index) SearchDocuments(ctx context.Context, agentID, root, query string, limit int) ([]DocumentResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []DocumentResult{}, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 40
	}
	var rows *sql.Rows
	var err error
	if utf8.RuneCountInString(query) >= 3 {
		literal := `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
		rows, err = i.db.QueryContext(ctx, `SELECT path,line,substr(content,1,300) FROM document_lines WHERE document_lines MATCH ? AND agent_id=? AND root=? LIMIT ?`, literal, agentID, root, limit)
	} else {
		escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
		rows, err = i.db.QueryContext(ctx, `SELECT path,line,substr(content,1,300) FROM document_lines WHERE content LIKE ? ESCAPE '\' AND agent_id=? AND root=? LIMIT ?`, `%`+escaped+`%`, agentID, root, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	results := make([]DocumentResult, 0)
	for rows.Next() {
		var result DocumentResult
		if err := rows.Scan(&result.Path, &result.Line, &result.Snippet); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}
