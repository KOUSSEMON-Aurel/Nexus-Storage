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
	ID         int64
	Path       string
	VideoID    string
	Size       int64
	Hash       string
	Key        string
	LastUpdate string
	Starred    bool
	DeletedAt  *string
	ParentID   *int64
	SHA256     string
	FileKey    string // V3: per-file encryption key (hex-encoded, encrypted with master)
	IsArchive  bool   // V3: true if the payload is a .tar archive
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
	
	// Check for FTS5 support
	hasFTS5 := false
	rows, _ := db.Query("PRAGMA compile_options")
	if rows != nil {
		for rows.Next() {
			var opt string
			rows.Scan(&opt)
			if opt == "ENABLE_FTS5" {
				hasFTS5 = true
			}
		}
		rows.Close()
	}

	if hasFTS5 {
		runMigrate("create_fts", `CREATE VIRTUAL TABLE IF NOT EXISTS files_fts USING fts5(path, content='files', content_rowid='id')`)
		runMigrate("trigger_ai", `CREATE TRIGGER IF NOT EXISTS files_ai AFTER INSERT ON files BEGIN
			INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
		END;`)
		runMigrate("trigger_ad", `CREATE TRIGGER IF NOT EXISTS files_ad AFTER DELETE ON files BEGIN
			INSERT INTO files_fts(files_fts, rowid, path) VALUES('delete', old.id, old.path);
		END;`)
		runMigrate("trigger_au", `CREATE TRIGGER IF NOT EXISTS files_au AFTER UPDATE ON files BEGIN
			INSERT INTO files_fts(files_fts, rowid, path) VALUES('delete', old.id, old.path);
			INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
		END;`)
	} else {
		log.Printf("⚠️  SQLite FTS5 module not found. Advanced search will be disabled.")
	}


	runMigrate("create_shards", `CREATE TABLE IF NOT EXISTS shards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
		video_id TEXT,
		position INTEGER
	)`)

	runMigrate("create_tasks", `CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		type INTEGER,
		file_path TEXT,
		mode TEXT,
		is_manifest BOOLEAN,
		status TEXT,
		progress REAL,
		created_at TIMESTAMP,
		parent_id INTEGER,
		sha256 TEXT
	)`)

	// V3 Migrations
	runMigrate("add_file_key", `ALTER TABLE files ADD COLUMN file_key TEXT DEFAULT ''`)
	runMigrate("add_is_archive", `ALTER TABLE files ADD COLUMN is_archive BOOLEAN DEFAULT 0`)
	runMigrate("create_cache_entries", `CREATE TABLE IF NOT EXISTS cache_entries (
		video_id    TEXT PRIMARY KEY,
		file_path   TEXT NOT NULL,
		size_bytes  INTEGER NOT NULL,
		last_access TIMESTAMP DEFAULT CURRENT_TIMESTAMP
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
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, deleted_at, last_update, parent_id, COALESCE(sha256,''), COALESCE(is_archive, 0)
		FROM files WHERE sha256 = ? LIMIT 1`, sha256).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.IsArchive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) SearchFiles(query string) ([]FileRecord, error) {
	// Try FTS5 first (fast, requires -tags fts5 at build time)
	rows, err := d.db.Query(`
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, deleted_at, last_update, parent_id, COALESCE(sha256,''), COALESCE(is_archive, 0)
		FROM files WHERE id IN (SELECT rowid FROM files_fts WHERE path MATCH ?)`, query+"*")
	if err != nil {
		// FTS5 not available — fall back to LIKE (slower but always works)
		log.Printf("⚠️  FTS5 unavailable, falling back to LIKE search: %v", err)
		rows, err = d.db.Query(`
			SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, CAST(deleted_at AS TEXT), CAST(last_update AS TEXT), parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
			FROM files WHERE path LIKE ? AND deleted_at IS NULL`, "%"+query+"%")
		if err != nil {
			return nil, err
		}
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive); err == nil {
			files = append(files, f)
		} else {
			log.Printf("⚠️  SearchFiles scan error: %v", err)
		}
	}
	return files, nil
}

func (d *Database) LogQuotaUsage(units int) {
	date := time.Now().Format("2006-01-02")
	d.db.Exec(`INSERT INTO quota_log (date, units) VALUES (?, ?) ON CONFLICT(date) DO UPDATE SET units = units + ?`, date, units, units)
}

func (d *Database) MergeManifest(cloudDbPath string) error {
	// 1. Attach the cloud DB
	_, err := d.db.Exec(fmt.Sprintf("ATTACH DATABASE '%s' AS cloud", cloudDbPath))
	if err != nil {
		return fmt.Errorf("attach failed: %w", err)
	}
	defer d.db.Exec("DETACH DATABASE cloud")

	// 1.5. Forward Compatibility: Apply missing columns to old manifests.
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN parent_id INTEGER`)
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN sha256 TEXT`)
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN file_key TEXT DEFAULT ''`)
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN is_archive BOOLEAN DEFAULT 0`)

	// 2. Merge Folders (Recursive Mapping)
	// We use a simplified level-by-level approach for folder sync.
	for i := 0; i < 5; i++ { 
		_, err = d.db.Exec(`
			INSERT INTO folders (name, parent_id)
			SELECT cf.name, lf_parent.id
			FROM cloud.folders cf
			LEFT JOIN cloud.folders cf_parent ON cf.parent_id = cf_parent.id
			LEFT JOIN folders lf_parent ON (cf_parent.name = lf_parent.name)
			WHERE NOT EXISTS (
				SELECT 1 FROM folders lf 
				WHERE lf.name = cf.name 
				AND (
					(lf.parent_id IS NULL AND cf.parent_id IS NULL) OR 
					(lf.parent_id = lf_parent.id)
				)
			)
			ON CONFLICT DO NOTHING
		`)
	}

	// 3. Merge Files using a CTE to resolve parent_ids correctly
	_, err = d.db.Exec(`
		WITH CloudFilesWithLocalParent AS (
			SELECT 
				cf.path, cf.video_id, cf.size, cf.hash, cf.key, cf.starred, cf.deleted_at, cf.last_update, cf.sha256, cf.file_key, cf.is_archive,
				lf.id as local_parent_id
			FROM cloud.files cf
			LEFT JOIN cloud.folders cfolder ON cf.parent_id = cfolder.id
			LEFT JOIN folders lf ON cfolder.name = lf.name
		)
		INSERT INTO files (path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256, file_key, is_archive)
		SELECT 
			path, video_id, size, hash, key, starred, deleted_at, last_update, local_parent_id, sha256, file_key, is_archive
		FROM CloudFilesWithLocalParent
		ON CONFLICT(path) DO UPDATE SET
			video_id    = excluded.video_id,
			size        = excluded.size,
			hash        = excluded.hash,
			key         = excluded.key,
			starred     = excluded.starred,
			deleted_at  = CASE WHEN files.deleted_at IS NOT NULL THEN files.deleted_at ELSE excluded.deleted_at END,
			last_update = excluded.last_update,
			parent_id   = excluded.parent_id,
			sha256      = excluded.sha256,
			file_key    = excluded.file_key,
			is_archive  = excluded.is_archive
		WHERE (excluded.last_update > files.last_update OR files.last_update IS NULL)
		  AND files.deleted_at IS NULL
	`)
	if err != nil {
		return fmt.Errorf("files merge failed: %w", err)
	}

	// 4. Merge Shards
	_, err = d.db.Exec(`
		INSERT INTO shards (file_id, video_id, position)
		SELECT f.id, cs.video_id, cs.position
		FROM cloud.shards cs
		JOIN cloud.files cf ON cs.file_id = cf.id
		JOIN files f ON f.path = cf.path
		ON CONFLICT DO NOTHING
	`)
	
	return err
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

func (d *Database) IsStealthMode() bool {
	val, ok := d.GetKV("stealth_mode")
	return ok && val == "true"
}

func (d *Database) SaveFile(path, videoID string, size int64, hash, key string, parentID *int64, sha256 string, isArchive bool) error {
	return d.SaveFileWithKey(path, videoID, size, hash, key, parentID, sha256, "", isArchive)
}

func (d *Database) SaveFileWithKey(path, videoID string, size int64, hash, key string, parentID *int64, sha256, fileKey string, isArchive bool) error {
	query := `
	INSERT INTO files (path, video_id, size, hash, key, parent_id, sha256, file_key, is_archive)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		video_id = excluded.video_id,
		size = excluded.size,
		hash = excluded.hash,
		key = excluded.key,
		parent_id = excluded.parent_id,
		sha256 = excluded.sha256,
		file_key = excluded.file_key,
		is_archive = excluded.is_archive,
		last_update = CURRENT_TIMESTAMP,
		deleted_at = NULL;
	`
	if _, err := d.db.Exec(query, path, videoID, size, hash, key, parentID, sha256, fileKey, isArchive); err != nil {
		return fmt.Errorf("could not save file: %w", err)
	}
	if d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return nil
}

// GetFileKey returns the per-file encryption key stored for a given file ID.
func (d *Database) GetFileKey(fileID int64) (string, error) {
	var key string
	err := d.db.QueryRow(`SELECT COALESCE(file_key, '') FROM files WHERE id = ?`, fileID).Scan(&key)
	return key, err
}

// SetFileKey updates the per-file encryption key for a given file ID.
func (d *Database) SetFileKey(fileID int64, fileKey string) error {
	_, err := d.db.Exec(`UPDATE files SET file_key = ? WHERE id = ?`, fileKey, fileID)
	return err
}

// ─── Cache DB Accessors ────────────────────────────────────────────────────────

type CacheEntry struct {
	VideoID    string
	FilePath   string
	SizeBytes  int64
	LastAccess string
}

func (d *Database) GetCacheEntry(videoID string) *CacheEntry {
	var e CacheEntry
	err := d.db.QueryRow(
		`SELECT video_id, file_path, size_bytes, last_access FROM cache_entries WHERE video_id = ?`,
		videoID).Scan(&e.VideoID, &e.FilePath, &e.SizeBytes, &e.LastAccess)
	if err != nil {
		return nil
	}
	return &e
}

func (d *Database) SaveCacheEntry(videoID, filePath string, sizeBytes int64) error {
	_, err := d.db.Exec(
		`INSERT INTO cache_entries (video_id, file_path, size_bytes, last_access) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(video_id) DO UPDATE SET file_path=excluded.file_path, size_bytes=excluded.size_bytes, last_access=CURRENT_TIMESTAMP`,
		videoID, filePath, sizeBytes)
	return err
}

func (d *Database) TouchCacheEntry(videoID string) {
	d.db.Exec(`UPDATE cache_entries SET last_access = CURRENT_TIMESTAMP WHERE video_id = ?`, videoID)
}

func (d *Database) DeleteCacheEntry(videoID string) {
	d.db.Exec(`DELETE FROM cache_entries WHERE video_id = ?`, videoID)
}

func (d *Database) OldestCacheEntry() *CacheEntry {
	var e CacheEntry
	err := d.db.QueryRow(
		`SELECT video_id, file_path, size_bytes FROM cache_entries ORDER BY last_access ASC LIMIT 1`).
		Scan(&e.VideoID, &e.FilePath, &e.SizeBytes)
	if err != nil {
		return nil
	}
	return &e
}

func (d *Database) CacheStats() (totalBytes int64, count int) {
	d.db.QueryRow(`SELECT COALESCE(SUM(size_bytes), 0), COUNT(*) FROM cache_entries`).Scan(&totalBytes, &count)
	return
}

func (d *Database) SaveTask(id string, tType int, filePath, mode string, isManifest bool, status string, progress float64, createdAt time.Time, parentID *int64, sha256 string) error {
	_, err := d.db.Exec(`
		INSERT INTO tasks (id, type, file_path, mode, is_manifest, status, progress, created_at, parent_id, sha256)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			status = excluded.status,
			progress = excluded.progress,
			sha256 = excluded.sha256
	`, id, tType, filePath, mode, isManifest, status, progress, createdAt, parentID, sha256)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

func (d *Database) GetPendingTasks() (*sql.Rows, error) {
	return d.db.Query(`SELECT id, type, file_path, mode, is_manifest, status, progress, created_at, parent_id, sha256 FROM tasks ORDER BY created_at ASC`)
}

func (d *Database) DeleteTask(id string) error {
	_, err := d.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

func (d *Database) SaveShard(fileID int64, videoID string, position int) error {
	_, err := d.db.Exec(`INSERT INTO shards (file_id, video_id, position) VALUES (?, ?, ?)`, fileID, videoID, position)
	return err
}

func (d *Database) GetShardsForFile(fileID int64) ([]string, error) {
	rows, err := d.db.Query(`SELECT video_id FROM shards WHERE file_id = ? ORDER BY position ASC`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var videoIDs []string
	for rows.Next() {
		var vid string
		if err := rows.Scan(&vid); err == nil {
			videoIDs = append(videoIDs, vid)
		}
	}
	return videoIDs, nil
}

func (d *Database) GetFile(path string) (*FileRecord, error) {
	var f FileRecord
	err := d.db.QueryRow(`
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, CAST(deleted_at AS TEXT), CAST(last_update AS TEXT), parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
		FROM files WHERE path = ?`, path).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) GetFileByID(id int64) (*FileRecord, error) {
	var f FileRecord
	err := d.db.QueryRow(`
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, CAST(deleted_at AS TEXT), CAST(last_update AS TEXT), parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
		FROM files WHERE id = ?`, id).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) ListFiles() ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, CAST(deleted_at AS TEXT), CAST(last_update AS TEXT), parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
		FROM files WHERE deleted_at IS NULL ORDER BY last_update DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive)
		if err == nil {
			files = append(files, f)
		} else {
			log.Printf("⚠️  ListFiles scan error: %v", err)
		}
	}
	return files, nil
}

func (d *Database) ListTrash() ([]FileRecord, error) {
	rows, err := d.db.Query(`
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, CAST(deleted_at AS TEXT), CAST(last_update AS TEXT), parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
		FROM files WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive)
		if err == nil {
			files = append(files, f)
		} else {
			log.Printf("⚠️  ListTrash scan error: %v", err)
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

// CleanupTrash removes files that have been in trash longer than 'days'.
func (d *Database) CleanupTrash(days int) ([]string, error) {
	threshold := time.Now().AddDate(0, 0, -days).Format("2006-01-02 15:04:05")
	
	// Find VideoIDs to also queue cloud deletion
	rows, err := d.db.Query(`SELECT video_id FROM files WHERE deleted_at < ? AND deleted_at IS NOT NULL`, threshold)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	
	var videoIDs []string
	for rows.Next() {
		var vid string
		if err := rows.Scan(&vid); err == nil && vid != "" {
			videoIDs = append(videoIDs, vid)
		}
	}

	_, err = d.db.Exec(`DELETE FROM files WHERE deleted_at < ? AND deleted_at IS NOT NULL`, threshold)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	
	return videoIDs, err
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
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, CAST(deleted_at AS TEXT), CAST(last_update AS TEXT), parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
		FROM files
		WHERE deleted_at IS NULL AND parent_id IS ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []FileRecord
	for rows.Next() {
		var f FileRecord
		err := rows.Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive)
		if err == nil {
			files = append(files, f)
		} else {
			log.Printf("⚠️  ListFilesByFolder scan error: %v", err)
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

// (scanFile and scanFiles were unused and removed in V3)
