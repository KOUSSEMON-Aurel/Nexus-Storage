package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	db             *sql.DB
	mu             sync.Mutex
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

	// V4 Migrations (Security: Password-based key derivation)
	runMigrate("create_recovery_state", `CREATE TABLE IF NOT EXISTS recovery_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		recovery_salt TEXT NOT NULL,
		manifest_revision INTEGER DEFAULT 1,
		last_backup_ts TEXT,
		recovery_packet_drive_id TEXT,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP
	)`)
	
	// Migration: Add recovery_packet_drive_id column if it doesn't exist
	runMigrate("add_recovery_packet_drive_id", `
		ALTER TABLE recovery_state ADD COLUMN recovery_packet_drive_id TEXT;
	`) // Safe: migration checks if column exists before adding

	runMigrate("create_tombstones", `CREATE TABLE IF NOT EXISTS tombstones (
		file_hash TEXT PRIMARY KEY,
		deleted_at_lsn INTEGER,
		deleted_at_ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)

	runMigrate("create_pending_sync", `CREATE TABLE IF NOT EXISTS pending_sync (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_path TEXT,
		lsn INTEGER,
		status TEXT DEFAULT 'pending',
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)

	// Initialize kv_store with default values if they don't exist
	d.db.Exec(`INSERT OR IGNORE INTO kv_store (key, value) VALUES ('manifest_version', '0')`)
	d.db.Exec(`INSERT OR IGNORE INTO kv_store (key, value) VALUES ('last_push_lsn', '0')`)
	d.db.Exec(`INSERT OR IGNORE INTO kv_store (key, value) VALUES ('last_push_hash', '')`)

	// Add trigger for tombstones on permanent delete
	d.db.Exec(`CREATE TRIGGER IF NOT EXISTS files_tombstone AFTER DELETE ON files
		BEGIN
			INSERT OR REPLACE INTO tombstones (file_hash, deleted_at_lsn, deleted_at_ts)
			VALUES (old.sha256, (SELECT value FROM kv_store WHERE key = 'manifest_version'), CURRENT_TIMESTAMP);
		END;`)

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
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, deleted_at, last_update, parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
		FROM files WHERE sha256 = ? LIMIT 1`, sha256).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

// GetFileByVideoID retrieves a file record by its primary video ID (first shard)
func (d *Database) GetFileByVideoID(videoID string) (*FileRecord, error) {
	var f FileRecord
	err := d.db.QueryRow(`
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, deleted_at, last_update, parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
		FROM files WHERE video_id = ? LIMIT 1`, videoID).Scan(&f.ID, &f.Path, &f.VideoID, &f.Size, &f.Hash, &f.Key, &f.Starred, &f.DeletedAt, &f.LastUpdate, &f.ParentID, &f.SHA256, &f.FileKey, &f.IsArchive)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) SearchFiles(query string) ([]FileRecord, error) {
	// Try FTS5 first (fast, requires -tags fts5 at build time)
	rows, err := d.db.Query(`
		SELECT id, path, COALESCE(video_id,''), size, hash, COALESCE(key,''), starred, deleted_at, last_update, parent_id, COALESCE(sha256,''), COALESCE(file_key,''), COALESCE(is_archive, 0)
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
	pt, _ := time.LoadLocation("America/Los_Angeles")
	date := time.Now().In(pt).Format("2006-01-02")
	d.db.Exec(`INSERT INTO quota_log (date, units) VALUES (?, ?) ON CONFLICT(date) DO UPDATE SET units = units + ?`, date, units, units)
}

func (d *Database) MergeManifest(driveManifestPath string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. Attach the cloud manifest database
	_, err := d.db.Exec(`ATTACH DATABASE ? AS cloud`, driveManifestPath)
	if err != nil {
		return fmt.Errorf("attach failed: %w", err)
	}
	defer d.db.Exec("DETACH DATABASE cloud")

	// 1.5. Forward Compatibility
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN parent_id INTEGER`)
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN sha256 TEXT`)
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN file_key TEXT DEFAULT ''`)
	d.db.Exec(`ALTER TABLE cloud.files ADD COLUMN is_archive BOOLEAN DEFAULT 0`)

	// 2. Merge Folders (Simplified level-by-level)
	for i := 0; i < 5; i++ { 
		_, err = d.db.Exec(`
			INSERT OR IGNORE INTO folders (name, parent_id)
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
		`)
	}

	// 3. Create a temporary table for file mapping (Compatibility Fix)
	d.db.Exec(`DROP TABLE IF EXISTS cloud_merge`)
	_, err = d.db.Exec(`
		CREATE TEMP TABLE cloud_merge AS 
		SELECT 
			cf.path, cf.video_id, cf.size, cf.hash, cf.key, cf.starred, cf.deleted_at, cf.last_update, cf.sha256, cf.file_key, cf.is_archive,
			lf.id as local_parent_id
		FROM cloud.files cf
		LEFT JOIN cloud.folders cfolder ON cf.parent_id = cfolder.id
		LEFT JOIN folders lf ON cfolder.name = lf.name
	`)
	if err != nil {
		return fmt.Errorf("temp table creation failed: %w", err)
	}

	// 4. Update existing files
	_, err = d.db.Exec(`
		UPDATE files SET
			video_id    = (SELECT video_id FROM cloud_merge WHERE cloud_merge.path = files.path),
			size        = (SELECT size FROM cloud_merge WHERE cloud_merge.path = files.path),
			hash        = (SELECT hash FROM cloud_merge WHERE cloud_merge.path = files.path),
			key         = (SELECT key FROM cloud_merge WHERE cloud_merge.path = files.path),
			starred     = (SELECT starred FROM cloud_merge WHERE cloud_merge.path = files.path),
			deleted_at  = (SELECT CASE WHEN files.deleted_at IS NOT NULL THEN files.deleted_at ELSE cloud_merge.deleted_at END FROM cloud_merge WHERE cloud_merge.path = files.path),
			last_update = (SELECT last_update FROM cloud_merge WHERE cloud_merge.path = files.path),
			parent_id   = (SELECT local_parent_id FROM cloud_merge WHERE cloud_merge.path = files.path),
			sha256      = (SELECT sha256 FROM cloud_merge WHERE cloud_merge.path = files.path),
			file_key    = (SELECT file_key FROM cloud_merge WHERE cloud_merge.path = files.path),
			is_archive  = (SELECT is_archive FROM cloud_merge WHERE cloud_merge.path = files.path)
		WHERE path IN (SELECT path FROM cloud_merge)
		  AND (SELECT last_update FROM cloud_merge WHERE cloud_merge.path = files.path) > files.last_update
		  AND files.deleted_at IS NULL
	`)
	
	// 5. Insert new files
	_, err = d.db.Exec(`
		INSERT INTO files (path, video_id, size, hash, key, starred, deleted_at, last_update, parent_id, sha256, file_key, is_archive)
		SELECT path, video_id, size, hash, key, starred, deleted_at, last_update, local_parent_id, sha256, file_key, is_archive
		FROM cloud_merge
		WHERE path NOT IN (SELECT path FROM files)
	`)

	if err != nil {
		return fmt.Errorf("files merge failed: %w", err)
	}

	// 6. Merge Shards
	_, err = d.db.Exec(`
		INSERT OR IGNORE INTO shards (file_id, video_id, position)
		SELECT f.id, cs.video_id, cs.position
		FROM cloud.shards cs
		JOIN cloud.files cf ON cs.file_id = cf.id
		JOIN files f ON f.path = cf.path
	`)
	
	return err
}


func (d *Database) GetDailyQuota() int {
	pt, _ := time.LoadLocation("America/Los_Angeles")
	date := time.Now().In(pt).Format("2006-01-02")
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
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`INSERT INTO kv_store (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

func (d *Database) GetKV(key string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
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

// ─── Recovery State (V4 Key Derivation) ───────────────────────────────────────

// GetRecoverySalt returns the recovery salt (hex-encoded).
// Used to generate the recovery_state table if not found, one row only.
func (d *Database) GetRecoverySalt() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var salt string
	err := d.db.QueryRow(`SELECT recovery_salt FROM recovery_state WHERE id = 1`).Scan(&salt)
	if err == sql.ErrNoRows {
		return "", nil // No salt set yet
	}
	return salt, err
}

// SetRecoverySalt stores the recovery salt (hex-encoded).
// Inserts or updates the single recovery_state row.
func (d *Database) SetRecoverySalt(saltHex string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`
		INSERT INTO recovery_state (id, recovery_salt, created_at) VALUES (1, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET recovery_salt = excluded.recovery_salt
	`, saltHex)
	return err
}

// GetManifestRevision returns the manifest revision number (default 1).
func (d *Database) GetManifestRevision() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var revision int
	err := d.db.QueryRow(`SELECT COALESCE(manifest_revision, 1) FROM recovery_state WHERE id = 1`).Scan(&revision)
	if err == sql.ErrNoRows {
		return 1, nil // Default
	}
	return revision, err
}

// IncrementManifestRevision increments the manifest revision (for password rotation tracking).
// Returns the new revision number.
func (d *Database) IncrementManifestRevision() (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`
		UPDATE recovery_state SET manifest_revision = manifest_revision + 1 WHERE id = 1
	`)
	if err != nil {
		return 0, err
	}
	// Fetch the new revision
	var revision int
	err = d.db.QueryRow(`SELECT COALESCE(manifest_revision, 1) FROM recovery_state WHERE id = 1`).Scan(&revision)
	return revision, err
}

// UpdateFileKey updates the encrypted file_key for a file (used in password rotation).
func (d *Database) UpdateFileKey(fileID int64, newFileKeyHex string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE files SET file_key = ? WHERE id = ?`, newFileKeyHex, fileID)
	return err
}

// SetLastManifestBackup records the timestamp of last successful Drive backup.
func (d *Database) SetLastManifestBackup(ts string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE recovery_state SET last_backup_ts = ? WHERE id = 1`, ts)
	return err
}

// GetRecoveryPacketDriveID retrieves the Drive file ID of the recovery packet.
func (d *Database) GetRecoveryPacketDriveID() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var driveID string
	err := d.db.QueryRow(`SELECT COALESCE(recovery_packet_drive_id, '') FROM recovery_state WHERE id = 1`).Scan(&driveID)
	if err != nil && err.Error() != "sql: no rows in result set" {
		return "", err
	}
	return driveID, nil
}

// SetRecoveryPacketDriveID stores the Drive file ID of the recovery packet.
func (d *Database) SetRecoveryPacketDriveID(driveID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE recovery_state SET recovery_packet_drive_id = ? WHERE id = 1`, driveID)
	return err
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
	d.IncrementLSN()
	d.AddPendingSync(path)
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
	d.mu.Lock()
	defer d.mu.Unlock()

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
	d.mu.Lock()
	defer d.mu.Unlock()

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
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(
		`UPDATE files SET deleted_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	d.IncrementLSN()
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

// Restore moves a file out of trash.
func (d *Database) Restore(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`UPDATE files SET deleted_at = NULL WHERE id = ?`, id)
	d.IncrementLSN()
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

// PermanentDelete removes the record entirely.
func (d *Database) PermanentDelete(id int64) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, err := d.db.Exec(`DELETE FROM files WHERE id = ?`, id)
	d.IncrementLSN()
	if err == nil && d.OnConfigChange != nil {
		d.OnConfigChange()
	}
	return err
}

// ─── Sync Support ─────────────────────────────────────────────────────────────

func (d *Database) IntegrityCheck() error {
	var res string
	err := d.db.QueryRow("PRAGMA integrity_check").Scan(&res)
	if err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", res)
	}
	return nil
}

func (d *Database) Checkpoint() error {
	// wal_checkpoint(RESTART) = flush to disk + reset WAL log
	// Then wal_checkpoint(TRUNCATE) = truncate WAL file to zero
	// This is more reliable than single TRUNCATE
	if _, err := d.db.Exec("PRAGMA wal_checkpoint(RESTART)"); err != nil {
		return fmt.Errorf("checkpoint restart failed: %w", err)
	}
	if _, err := d.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("checkpoint truncate failed: %w", err)
	}
	return nil
}

func (d *Database) GetLocalLSN() (int64, error) {
	val, ok := d.GetKV("manifest_version")
	if !ok {
		return 0, nil
	}
	var lsn int64
	fmt.Sscanf(val, "%d", &lsn)
	return lsn, nil
}

func (d *Database) IncrementLSN() (int64, error) {
	lsn, _ := d.GetLocalLSN()
	lsn++
	err := d.SetKV("manifest_version", fmt.Sprintf("%d", lsn))
	return lsn, err
}

func (d *Database) GetLastPushInfo() (int64, string, error) {
	lsnVal, _ := d.GetKV("last_push_lsn")
	hashVal, _ := d.GetKV("last_push_hash")
	var lsn int64
	fmt.Sscanf(lsnVal, "%d", &lsn)
	return lsn, hashVal, nil
}

func (d *Database) UpdatePushStatus(lsn int64, hash string) error {
	if err := d.SetKV("last_push_lsn", fmt.Sprintf("%d", lsn)); err != nil {
		return err
	}
	return d.SetKV("last_push_hash", hash)
}

func (d *Database) GetTotalFileCount() (int64, error) {
	var count int64
	err := d.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&count)
	return count, err
}

func (d *Database) ClearPendingSync() error {
	_, err := d.db.Exec("DELETE FROM pending_sync")
	return err
}

func (d *Database) AddPendingSync(path string) error {
	lsn, _ := d.GetLocalLSN()
	_, err := d.db.Exec("INSERT INTO pending_sync (file_path, lsn) VALUES (?, ?)", path, lsn)
	return err
}

func (d *Database) HasFailedSyncs() (bool, error) {
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM pending_sync WHERE status = 'failed'").Scan(&count)
	return count > 0, err
}

// CleanupTrash removes files that have been in trash longer than 'days'.
func (d *Database) CleanupTrash(days int) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
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
	d.mu.Lock()
	defer d.mu.Unlock()
	res, err := d.db.Exec(`INSERT OR IGNORE INTO folders (name, parent_id) VALUES (?, ?)`, name, parentID)
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
	d.mu.Lock()
	defer d.mu.Unlock()
	var f FolderRecord
	err := d.db.QueryRow(`SELECT id, name, parent_id, playlist_id FROM folders WHERE id = ?`, id).Scan(&f.ID, &f.Name, &f.ParentID, &f.PlaylistID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func (d *Database) ListSubfolders(parentID *int64) ([]FolderRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rows, err := d.db.Query(`SELECT id, name, parent_id, playlist_id FROM folders WHERE parent_id IS ?`, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []FolderRecord
	for rows.Next() {
		var f FolderRecord
		if err := rows.Scan(&f.ID, &f.Name, &f.ParentID, &f.PlaylistID); err == nil {
			folders = append(folders, f)
		}
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
	d.mu.Lock()
	defer d.mu.Unlock()
	var s Stats
	// Direct queries for reliability
	d.db.QueryRow(`SELECT COUNT(*), IFNULL(SUM(size),0) FROM files WHERE deleted_at IS NULL`).Scan(&s.FileCount, &s.TotalSize)
	d.db.QueryRow(`SELECT COUNT(*) FROM files WHERE starred = 1 AND deleted_at IS NULL`).Scan(&s.StarredCount)
	d.db.QueryRow(`SELECT COUNT(*) FROM files WHERE deleted_at IS NOT NULL`).Scan(&s.TrashCount)
	return s, nil
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// (scanFile and scanFiles were unused and removed in V3)
