package main

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	db             *sql.DB
	OnConfigChange func()
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
	ParentID  *int64
}

type FolderRecord struct {
	ID       int64
	Name     string
	ParentID *int64
}

func (d *Database) Init(dbPath string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("could not open database: %w", err)
	}
	d.db = db

	query := `
	PRAGMA journal_mode=WAL;
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
		`CREATE TABLE IF NOT EXISTS kv_store (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE IF NOT EXISTS folders (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			parent_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
			UNIQUE(name, parent_id)
		)`,
		`ALTER TABLE files ADD COLUMN parent_id INTEGER REFERENCES folders(id) ON DELETE CASCADE`,
	}
	for _, m := range migrations {
		db.Exec(m) // Ignore errors (already exists)
	}

	return nil
}

func (d *Database) Close() {
	if d.db != nil {
		d.db.Close()
	}
}

func (d *Database) SetKV(key, value string) error {
	_, err := d.db.Exec(`INSERT INTO kv_store (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

func (d *Database) GetKV(key string) (string, bool) {
	var value string
	err := d.db.QueryRow(`SELECT value FROM kv_store WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", false
	}
	return value, true
}

func (d *Database) SaveFile(path, videoID string, size int64, hash, key string, parentID *int64) error {
	query := `
	INSERT INTO files (path, video_id, size, hash, key, parent_id)
	VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		video_id = excluded.video_id,
		size = excluded.size,
		hash = excluded.hash,
		key = excluded.key,
		parent_id = excluded.parent_id,
		last_update = CURRENT_TIMESTAMP;
	`
	if _, err := d.db.Exec(query, path, videoID, size, hash, key, parentID); err != nil {
		return fmt.Errorf("could not save file: %w", err)
	}
	if d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return nil
}

func (d *Database) GetFile(path string) (*FileRecord, error) {
	row := d.db.QueryRow(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id
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
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

// RestoreFile moves a file out of trash.
func (d *Database) RestoreFile(id int64) error {
	_, err := d.db.Exec(`UPDATE files SET deleted_at = NULL WHERE id = ?`, id)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

// PermanentDelete removes the record entirely.
func (d *Database) PermanentDelete(id int64) error {
	_, err := d.db.Exec(`DELETE FROM files WHERE id = ?`, id)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

// ToggleStar sets the starred status.
// CreateFolder ensures a folder exists and returns its ID.
func (d *Database) CreateFolder(name string, parentID *int64) (int64, error) {
	res, err := d.db.Exec(`INSERT INTO folders (name, parent_id) VALUES (?, ?) ON CONFLICT(name, parent_id) DO UPDATE SET name=name`, name, parentID)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// Conflict happened, fetch the existing ID
		err = d.db.QueryRow(`SELECT id FROM folders WHERE name = ? AND parent_id IS ?`, name, parentID).Scan(&id)
	}
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return id, err
}

func (d *Database) GetFolderByID(id int64) (*FolderRecord, error) {
	var f FolderRecord
	err := d.db.QueryRow(`SELECT id, name, parent_id FROM folders WHERE id = ?`, id).Scan(&f.ID, &f.Name, &f.ParentID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) ListSubfolders(parentID *int64) ([]FolderRecord, error) {
	rows, err := d.db.Query(`SELECT id, name, parent_id FROM folders WHERE parent_id IS ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []FolderRecord
	for rows.Next() {
		var f FolderRecord
		rows.Scan(&f.ID, &f.Name, &f.ParentID)
		folders = append(folders, f)
	}
	return folders, nil
}

func (d *Database) ListFilesByFolder(parentID *int64) ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id
		FROM files
		WHERE deleted_at IS NULL AND parent_id IS ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

func (d *Database) ToggleStar(id int64, starred bool) error {
	v := 0
	if starred {
		v = 1
	}
	_, err := d.db.Exec(`UPDATE files SET starred = ? WHERE id = ?`, v, id)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
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
		&fr.Key, &fr.Starred, &fr.DeletedAt, &fr.LastUpdate, &fr.ParentID); err != nil {
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
			&fr.Key, &fr.Starred, &fr.DeletedAt, &fr.LastUpdate, &fr.ParentID); err != nil {
			return nil, err
		}
		files = append(files, fr)
	}
	return files, nil
}
