package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Store is the quarantine SQLite store.
type Store struct {
	db *DB
}

// OpenStore opens (or creates) a quarantine database at dbPath.
func OpenStore(dbPath string) (*Store, error) {
	db, err := OpenDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open quarantine db: %w", err)
	}
	if err := ensureSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("ensure schema: %w", err)
	}
	return &Store{db: db}, nil
}

func ensureSchema(db *DB) error {
	return db.Exec(`
		CREATE TABLE IF NOT EXISTS items (
			id          TEXT PRIMARY KEY,
			created_at  TEXT NOT NULL,
			updated_at  TEXT NOT NULL,
			status      TEXT NOT NULL DEFAULT 'pending',
			kind        TEXT NOT NULL,
			source      TEXT NOT NULL,
			label       TEXT,
			content     TEXT NOT NULL,
			target_path TEXT,
			metadata    TEXT,
			reviewed_by TEXT,
			review_note TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_items_status  ON items(status);
		CREATE INDEX IF NOT EXISTS idx_items_kind    ON items(kind);
		CREATE INDEX IF NOT EXISTS idx_items_source  ON items(source);
		CREATE INDEX IF NOT EXISTS idx_items_created ON items(created_at);
	`)
}

// Push inserts a new pending item. Returns the created item.
func (s *Store) Push(req PushRequest, source string) (*Item, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339Nano)

	var metadataStr string
	if req.Metadata != nil {
		b, err := json.Marshal(req.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
		metadataStr = string(b)
	}

	stmt, err := s.db.Prepare(`
		INSERT INTO items (id, created_at, updated_at, status, kind, source, label, content, target_path, metadata)
		VALUES (?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	defer stmt.Finalize()

	stmt.BindText(1, id)
	stmt.BindText(2, nowStr)
	stmt.BindText(3, nowStr)
	stmt.BindText(4, string(req.Kind))
	stmt.BindText(5, source)
	stmt.BindTextOrNull(6, req.Label)
	stmt.BindText(7, req.Content)
	stmt.BindTextOrNull(8, req.TargetPath)
	stmt.BindTextOrNull(9, metadataStr)

	if _, err := stmt.Step(); err != nil {
		return nil, fmt.Errorf("insert item: %w", err)
	}

	return s.Get(id)
}

// Get retrieves a single item by ID, or nil if not found.
func (s *Store) Get(id string) (*Item, error) {
	stmt, err := s.db.Prepare(`SELECT id, created_at, updated_at, status, kind, source, label, content, target_path, metadata, reviewed_by, review_note FROM items WHERE id = ?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Finalize()

	stmt.BindText(1, id)
	hasRow, err := stmt.Step()
	if err != nil {
		return nil, err
	}
	if !hasRow {
		return nil, nil
	}

	return scanItemFromStmt(stmt), nil
}

// List returns items matching the given filter.
func (s *Store) List(f ListFilter) (*ListResult, error) {
	var conditions []string
	var params []string

	if f.Status != "" {
		conditions = append(conditions, "status = ?")
		params = append(params, string(f.Status))
	}
	if f.Kind != "" {
		conditions = append(conditions, "kind = ?")
		params = append(params, string(f.Kind))
	}
	if f.Source != "" {
		conditions = append(conditions, "source = ?")
		params = append(params, f.Source)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Count.
	countStmt, err := s.db.Prepare("SELECT COUNT(*) FROM items " + where)
	if err != nil {
		return nil, err
	}
	defer countStmt.Finalize()
	for i, p := range params {
		countStmt.BindText(i+1, p)
	}
	countStmt.Step()
	total := countStmt.ColumnInt(0)

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(
		"SELECT id, created_at, updated_at, status, kind, source, label, content, target_path, metadata, reviewed_by, review_note FROM items %s ORDER BY created_at DESC LIMIT %d OFFSET %d",
		where, limit, offset,
	)
	listStmt, err := s.db.Prepare(query)
	if err != nil {
		return nil, err
	}
	defer listStmt.Finalize()
	for i, p := range params {
		listStmt.BindText(i+1, p)
	}

	var items []Item
	for {
		hasRow, err := listStmt.Step()
		if err != nil {
			return nil, err
		}
		if !hasRow {
			break
		}
		items = append(items, *scanItemFromStmt(listStmt))
	}
	if items == nil {
		items = []Item{}
	}

	return &ListResult{Items: items, Total: total}, nil
}

// Review updates an item's status to approved or rejected.
func (s *Store) Review(id string, req ReviewRequest) (*Item, error) {
	existing, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	if existing.Status != StatusPending {
		return nil, fmt.Errorf("item %s already reviewed (status: %s)", id, existing.Status)
	}

	decision := ItemStatus(req.Decision)
	if decision != StatusApproved && decision != StatusRejected {
		return nil, fmt.Errorf("decision must be 'approved' or 'rejected'")
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	stmt, err := s.db.Prepare(`UPDATE items SET status = ?, updated_at = ?, reviewed_by = ?, review_note = ? WHERE id = ?`)
	if err != nil {
		return nil, err
	}
	defer stmt.Finalize()

	stmt.BindText(1, string(decision))
	stmt.BindText(2, now)
	stmt.BindTextOrNull(3, req.ReviewedBy)
	stmt.BindTextOrNull(4, req.ReviewNote)
	stmt.BindText(5, id)

	if _, err := stmt.Step(); err != nil {
		return nil, err
	}

	return s.Get(id)
}

// Delete permanently removes an item. Returns true if it existed.
func (s *Store) Delete(id string) (bool, error) {
	stmt, err := s.db.Prepare(`DELETE FROM items WHERE id = ?`)
	if err != nil {
		return false, err
	}
	defer stmt.Finalize()
	stmt.BindText(1, id)
	if _, err := stmt.Step(); err != nil {
		return false, err
	}
	return s.db.Changes() > 0, nil
}

// Purge deletes all items with the given status. Returns count deleted.
func (s *Store) Purge(status ItemStatus) (int, error) {
	if status == "" {
		if err := s.db.Exec(`DELETE FROM items`); err != nil {
			return 0, err
		}
	} else {
		stmt, err := s.db.Prepare(`DELETE FROM items WHERE status = ?`)
		if err != nil {
			return 0, err
		}
		defer stmt.Finalize()
		stmt.BindText(1, string(status))
		if _, err := stmt.Step(); err != nil {
			return 0, err
		}
	}
	return s.db.Changes(), nil
}

// Count returns the number of items, optionally filtered by status.
func (s *Store) Count(status ItemStatus) (int, error) {
	var query string
	if status == "" {
		query = `SELECT COUNT(*) FROM items`
	} else {
		query = `SELECT COUNT(*) FROM items WHERE status = ?`
	}
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return 0, err
	}
	defer stmt.Finalize()
	if status != "" {
		stmt.BindText(1, string(status))
	}
	stmt.Step()
	return stmt.ColumnInt(0), nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- helpers ---

func scanItemFromStmt(stmt *Stmt) *Item {
	item := &Item{
		ID:         stmt.ColumnText(0),
		Status:     ItemStatus(stmt.ColumnText(3)),
		Kind:       ContentKind(stmt.ColumnText(4)),
		Source:     stmt.ColumnText(5),
		Label:      stmt.ColumnText(6),
		Content:    stmt.ColumnText(7),
		TargetPath: stmt.ColumnText(8),
		Metadata:   stmt.ColumnText(9),
		ReviewedBy: stmt.ColumnText(10),
		ReviewNote: stmt.ColumnText(11),
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, stmt.ColumnText(1))
	item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, stmt.ColumnText(2))
	return item
}
