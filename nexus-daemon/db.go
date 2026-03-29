package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

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
	Starred    bool
	DeletedAt  *string
	ParentID   *int64
	SHA256     string
}

type FolderRecord struct {
	ID         int64
	Name       string
	ParentID   *int64
	PlaylistID *string
}

func (d *Database) Init(dbPath string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("could not open database: %w", err)
	}
	d.db = db

	// Enable WAL Mode for concurrency
	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		log.Printf("⚠️  Could not set WAL mode: %v", err)
	}

	// Schema V1 (Core)
	query := `
	CREATE TABLE IF NOT EXISTS files (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		path       TEXT UNIQUE,
		video_id   TEXT,
		size       INTEGER,
		hash       TEXT,
		key        TEXT,
		last_update TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("could not create base files table: %w", err)
	}

	// Migrations (Idempotent)
	runMigrate := func(name, sqlStr string) {
		_, err := db.Exec(sqlStr)
		if err != nil {
			// We only log if it's NOT a "duplicate column" error
			if !strings.Contains(err.Error(), "duplicate column") && !strings.Contains(err.Error(), "already exists") {
				log.Printf("⚠️  Migration [%s] failed: %v", name, err)
			}
		} else {
			log.Printf("✅ Migration [%s] applied successfully", name)
		}
	}

	runMigrate("add_starred", `ALTER TABLE files ADD COLUMN starred BOOLEAN DEFAULT 0`)
	runMigrate("add_deleted_at", `ALTER TABLE files ADD COLUMN deleted_at TIMESTAMP`)
	runMigrate("create_kv_store", `CREATE TABLE IF NOT EXISTS kv_store (key TEXT PRIMARY KEY, value TEXT)`)
	runMigrate("create_folders", `CREATE TABLE IF NOT EXISTS folders (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		parent_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
		playlist_id TEXT,
		UNIQUE(name, parent_id)
	)`)
	runMigrate("add_parent_id", `ALTER TABLE files ADD COLUMN parent_id INTEGER REFERENCES folders(id) ON DELETE CASCADE`)
	runMigrate("add_sha256", `ALTER TABLE files ADD COLUMN sha256 TEXT`)
	runMigrate("create_meta_sync", `CREATE TABLE IF NOT EXISTS meta_sync (key TEXT PRIMARY KEY, value TEXT, last_sync TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	runMigrate("create_quota_log", `CREATE TABLE IF NOT EXISTS quota_log (date TEXT PRIMARY KEY, units INTEGER DEFAULT 0)`)
	
	// FTS5 & Triggers (Crucial for V2 Search)
	runMigrate("create_fts", `CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts5(path, content='files', content_rowid='id')`)
	
	runMigrate("trigger_ai", `CREATE TRIGGER IF NOT EXISTS files_ai AFTER INSERT ON files BEGIN
		INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
	END`)
	runMigrate("trigger_ad", `CREATE TRIGGER IF NOT EXISTS files_ad AFTER DELETE ON files BEGIN
		INSERT INTO files_fts(files_fts, rowid, path) VALUES('delete', old.id, old.path);
	END`)
	runMigrate("trigger_au", `CREATE TRIGGER IF NOT EXISTS files_au AFTER UPDATE ON files BEGIN
		INSERT INTO files_fts(files_fts, rowid, path) VALUES('delete', old.id, old.path);
		INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
	END`)

	runMigrate("create_shards", `CREATE TABLE IF NOT EXISTS shards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
		video_id TEXT,
		position INTEGER
	)`)

	// Final check: if files_fts is empty but files has data, rebuild it
	var count int
	db.QueryRow("SELECT count(*) FROM files_fts").Scan(&count)
	if count == 0 {
		var totalFiles int
		db.QueryRow("SELECT count(*) FROM files").Scan(&totalFiles)
		if totalFiles > 0 {
			log.Printf("🔄 Rebuilding search index (Found %d files missing from FTS)", totalFiles)
			db.Exec("INSERT INTO files_fts(files_fts) VALUES('rebuild')")
		}
	}

	return nil
}

func (d *Database) GetFileByHash(sha256 string) (*FileRecord, error) {
	var f FileRecord
	err := d.db.QueryRow(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
		FROM files WHERE sha256 = ? LIMIT 1`, sha256).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) SearchFiles(query string) ([]FileRecord, error) {
	// Try FTS5 first (fast, requires -tags fts5 at build time)
	rows, err := d.db.Query(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
		FROM files WHERE id IN (SELECT rowid FROM files_fts WHERE path MATCH ?)`, query+"*")
	if err != nil {
		// FTS5 not available — fall back to LIKE (slower but always works)
		log.Printf("⚠️  FTS5 unavailable, falling back to LIKE search: %v", err)
		rows, err = d.db.Query(`
			SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
			FROM files WHERE path LIKE ? AND deleted_at IS NULL`, "%"+query+"%")
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256); err == nil {
			files = append(files, f)
		}
	}
	return files, nil
}

func (d *Database) LogQuotaUsage(units int) {
	date := time.Now().Format("2006-01-02")
	d.db.Exec(`INSERT INTO quota_log (date, units) VALUES (?, ?) ON CONFLICT(date) DO UPDATE SET units = units + ?`, date, units, units)
}

func (d *Database) GetDailyQuota() int {
	date := time.Now().Format("2006-01-02")
	var units int
	d.db.QueryRow(`SELECT units FROM quota_log WHERE date = ?`, date).Scan(&units)
	return units
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

func (d *Database) SaveFile(path, videoID string, size int64, hash, key string, parentID *int64, sha256 string) error {
	query := `
	INSERT INTO files (path, video_id, size, hash, key, parent_id, sha256)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		video_id = excluded.video_id,
		size = excluded.size,
		hash = excluded.hash,
		key = excluded.key,
		parent_id = excluded.parent_id,
		sha256 = excluded.sha256,
		last_update = CURRENT_TIMESTAMP;
	`
	if _, err := d.db.Exec(query, path, videoID, size, hash, key, parentID, sha256); err != nil {
		return fmt.Errorf("could not save file: %w", err)
	}
	return nil
}

func (d *Database) GetFile(path string) (*FileRecord, error) {
	var f FileRecord
	err := d.db.QueryRow(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
		FROM files WHERE path = ?`, path).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) GetFileByID(id int64) (*FileRecord, error) {
	var f FileRecord
	err := d.db.QueryRow(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
		FROM files WHERE id = ?`, id).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) ListFiles() ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
		FROM files WHERE deleted_at IS NULL ORDER BY last_update DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256)
		if err == nil {
			files = append(files, f)
		}
	}
	return files, nil
}

func (d *Database) ListTrash() ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
		FROM files WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256)
		if err == nil {
			files = append(files, f)
		}
	}
	return files, nil
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

// Restore moves a file out of trash.
func (d *Database) Restore(id int64) error {
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
	err := d.db.QueryRow(`SELECT id, name, parent_id, playlist_id FROM folders WHERE id = ?`, id).Scan(&f.ID, &f.Name, &f.ParentID, &f.PlaylistID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) ListSubfolders(parentID *int64) ([]FolderRecord, error) {
	rows, err := d.db.Query(`SELECT id, name, parent_id, playlist_id FROM folders WHERE parent_id IS ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []FolderRecord
	for rows.Next() {
		var f FolderRecord
		rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.PlaylistID)
		folders = append(folders, f)
	}
	return folders, nil
}

func (d *Database) ListFilesByFolder(parentID *int64) ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256
		FROM files
		WHERE deleted_at IS NULL AND parent_id IS ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256)
		if err == nil {
			files = append(files, f)
		}
	}
	return files, nil
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
	// Direct queries for reliability
	d.db.QueryRow(`SELECT COUNT(*), IFNULL(SUM(size),0) FROM files WHERE deleted_at IS NULL`).Scan(&s.FileCount, &s.TotalSize)
	d.db.QueryRow(`SELECT COUNT(*) FROM files WHERE starred = 1 AND deleted_at IS NULL`).Scan(&s.StarredCount)
	d.db.QueryRow(`SELECT COUNT(*) FROM files WHERE deleted_at IS NOT NULL`).Scan(&s.TrashCount)
	return s, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func scanFile(row *sql.Row) (*FileRecord, error) {
	var fr FileRecord
	if err := row.Scan(&fr.ID, &fr.Path, &fr.VideoID, &fr.Size, &fr.Hash,
		&fr.Key, &fr.Starred, &fr.DeletedAt, &fr.LastUpdate, &fr.ParentID, &fr.SHA256); err != nil {
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
			&fr.Key, &fr.Starred, &fr.DeletedAt, &fr.LastUpdate, &fr.ParentID, &fr.SHA256); err != nil {
			return nil, err
		}
		files = append(files, fr)
	}
	return files, nil
}
