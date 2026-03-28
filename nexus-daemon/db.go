package main

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	db *sql.DB
}

type FileRecord struct {
	ID        int64
	Path      string
	VideoID   string
	Size      int64
	Hash      string
	Key       string
	LastUpdate string
	Starred   bool
	DeletedAt  *string
}

func (d *Database) Init(dbPath string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("could not open database: %w", err)
	}
	d.db = db

	query := `
	CREATE TABLE IF NOT EXISTS files (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		path       TEXT UNIQUE,
		video_id   TEXT,
		size       INTEGER,
		hash       TEXT,
		key        TEXT,
		starred    BOOLEAN DEFAULT 0,
		deleted_at TIMESTAMP,
		last_update TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("could not create tables: %w", err)
	}

	// Migrations for existing DBs (idempotent)
	migrations := []string{
		`ALTER TABLE files ADD COLUMN starred BOOLEAN DEFAULT 0`,
		`ALTER TABLE files ADD COLUMN deleted_at TIMESTAMP`,
	}
	for _, m := range migrations {
		db.Exec(m) // Ignore errors (column already exists)
	}

	return nil
}

func (d *Database) Close() {
	if d.db != nil {
		d.db.Close()
	}
}

func (d *Database) SaveFile(path, videoID string, size int64, hash, key string) error {
	query := `
	INSERT INTO files (path, video_id, size, hash, key)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		video_id = excluded.video_id,
		size = excluded.size,
		hash = excluded.hash,
		key = excluded.key,
		last_update = CURRENT_TIMESTAMP;
	`
	if _, err := d.db.Exec(query, path, videoID, size, hash, key); err != nil {
		return fmt.Errorf("could not save file: %w", err)
	}
	return nil
}

func (d *Database) GetFile(path string) (*FileRecord, error) {
	row := d.db.QueryRow(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update
		FROM files WHERE path = ?`, path)
	return scanFile(row)
}

func (d *Database) GetFileByID(id int64) (*FileRecord, error) {
	row := d.db.QueryRow(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update
		FROM files WHERE id = ?`, id)
	return scanFile(row)
}

func (d *Database) ListFiles() ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update
		FROM files
		WHERE deleted_at IS NULL
		ORDER BY last_update DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

func (d *Database) ListTrash() ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update
		FROM files
		WHERE deleted_at IS NOT NULL
		ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

// SoftDelete moves a file to trash.
func (d *Database) SoftDelete(id int64) error {
	_, err := d.db.Exec(
		`UPDATE files SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

// RestoreFile moves a file out of trash.
func (d *Database) RestoreFile(id int64) error {
	_, err := d.db.Exec(`UPDATE files SET deleted_at = NULL WHERE id = ?`, id)
	return err
}

// PermanentDelete removes the record entirely.
func (d *Database) PermanentDelete(id int64) error {
	_, err := d.db.Exec(`DELETE FROM files WHERE id = ?`, id)
	return err
}

// ToggleStar sets the starred status.
func (d *Database) ToggleStar(id int64, starred bool) error {
	v := 0
	if starred {
		v = 1
	}
	_, err := d.db.Exec(`UPDATE files SET starred = ? WHERE id = ?`, v, id)
	return err
}

type Stats struct {
	FileCount   int64 `json:"file_count"`
	TotalSize   int64 `json:"total_size"`
	StarredCount int64 `json:"starred_count"`
	TrashCount  int64 `json:"trash_count"`
}

func (d *Database) GetStats() (Stats, error) {
	var s Stats
	err := d.db.QueryRow(`
		SELECT
			COUNT(*) FILTER (WHERE deleted_at IS NULL),
			COALESCE(SUM(size) FILTER (WHERE deleted_at IS NULL), 0),
			COUNT(*) FILTER (WHERE starred = 1 AND deleted_at IS NULL),
			COUNT(*) FILTER (WHERE deleted_at IS NOT NULL)
		FROM files
	`).Scan(&s.FileCount, &s.TotalSize, &s.StarredCount, &s.TrashCount)
	if err != nil {
		// Fallback for older SQLite without FILTER
		err = d.db.QueryRow(
			`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files WHERE deleted_at IS NULL`,
		).Scan(&s.FileCount, &s.TotalSize)
	}
	return s, err
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func scanFile(row *sql.Row) (*FileRecord, error) {
	var fr FileRecord
	if err := row.Scan(&fr.ID, &fr.Path, &fr.VideoID, &fr.Size, &fr.Hash,
		&fr.Key, &fr.Starred, &fr.DeletedAt, &fr.LastUpdate); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &fr, nil
}

func scanFiles(rows *sql.Rows) ([]FileRecord, error) {
	var files []FileRecord
	for rows.Next() {
		var fr FileRecord
		if err := rows.Scan(&fr.ID, &fr.Path, &fr.VideoID, &fr.Size, &fr.Hash,
			&fr.Key, &fr.Starred, &fr.DeletedAt, &fr.LastUpdate); err != nil {
			return nil, err
		}
		files = append(files, fr)
	}
	return files, nil
}
