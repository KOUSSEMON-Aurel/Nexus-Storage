package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"sync"
	"time"

	"google.golang.org/api/drive/v3"
)

type SyncManifest struct {
	LSN          int64          `json:"lsn"`
	LogicalHash  string         `json:"logical_hash"`
	LastModified string         `json:"last_modified"`
	Stats        map[string]int `json:"stats"`
	PushedAt     string         `json:"pushed_at"`
}

type SyncManager struct {
	db     *Database
	yt     *YouTubeManager
	pm     *PlaylistManager
	dbPath string
	mu     sync.Mutex
}

func NewSyncManager(db *Database, yt *YouTubeManager, pm *PlaylistManager, dbPath string) *SyncManager {
	return &SyncManager{
		db:     db,
		yt:     yt,
		pm:     pm,
		dbPath: dbPath,
	}
}

// retryWithBackoff retries fn up to maxAttempts times with exponential backoff
// Spec: Retry transient errors (network, timeouts) without overwhelming the service
func (s *SyncManager) retryWithBackoff(maxAttempts int, opName string, fn func(context.Context) error) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		err := fn(ctx)
		cancel()

		if err == nil {
			return nil
		}

		lastErr = err
		if attempt < maxAttempts {
			backoffDur := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			log.Printf("⚠️  %s attempt %d failed: %v. Retrying in %v...", opName, attempt, err, backoffDur)
			time.Sleep(backoffDur)
		}
	}
	return fmt.Errorf("%s failed after %d attempts: %w", opName, maxAttempts, lastErr)
}

// PushDBToDrive implements the strict push logic
func (s *SyncManager) PushDBToDrive() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.Printf("🔄 Starting strict DB push to Drive...")

	// 0. Check pending_sync for failed entries
	if failed, err := s.db.HasFailedSyncs(); err == nil && failed {
		return fmt.Errorf("push forbidden: pending_sync contains failed entries that must be resolved first")
	}

	// 1. Integrity Check
	if err := s.db.IntegrityCheck(); err != nil {
		return fmt.Errorf("pre-push integrity check failed: %w", err)
	}

	// 2. Checkpoint WAL
	if err := s.db.Checkpoint(); err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}

	// Verify WAL is empty
	walPath := s.dbPath + "-wal"
	if info, err := os.Stat(walPath); err == nil && info.Size() > 0 {
		log.Printf("⚠️  WAL file still has %d bytes after checkpoint (may be normal with concurrent access)", info.Size())
	}

	// Note: .db-shm (shared memory file) may exist even after checkpoint in WAL mode
	// This is normal and expected - it's used by SQLite for process coordination
	// We do NOT require it to be empty or deleted
	shmPath := s.dbPath + "-shm"
	if info, err := os.Stat(shmPath); err == nil {
		log.Printf("ℹ️  WAL shared memory (.db-shm) exists (%d bytes) - this is normal in WAL mode", info.Size())
	}

	// 3. Local LSN Check
	localLSN, err := s.db.GetLocalLSN()
	if err != nil {
		return err
	}
	if localLSN == 0 {
		return fmt.Errorf("cannot push empty database (LSN=0)")
	}

	// 4. Read Remote Manifest
	remoteManifest, err := s.GetRemoteManifest()
	if err != nil {
		log.Printf("ℹ️  Could not read remote manifest (first backup?): %v", err)
	}

	if remoteManifest != nil {
		// 5. Smart Decision Logic
		stats, _ := s.db.GetSyncStats()
		logicalHash, _ := s.db.CalculateLogicalHash()
		lastModified, _ := s.db.GetLastModified()

		// Rule 2: In Sync check
		statsEqual := stats["files"] == remoteManifest.Stats["files"] &&
			stats["folders"] == remoteManifest.Stats["folders"] &&
			stats["tasks"] == remoteManifest.Stats["tasks"]

		if statsEqual && remoteManifest.LogicalHash == logicalHash {
			log.Printf("✅ DB already in sync (LSN %d)", localLSN)
			return nil
		}

		// Rule 3 & 4: Row Counts
		localTotal := stats["files"] + stats["folders"] + stats["tasks"]
		remoteTotal := remoteManifest.Stats["files"] + remoteManifest.Stats["folders"] + remoteManifest.Stats["tasks"]

		if localTotal > remoteTotal {
			log.Printf("🚀 Local is richer (%d > %d). Pushing...", localTotal, remoteTotal)
			// Proceed to push
		} else if remoteTotal > localTotal {
			return fmt.Errorf("remote is richer (%d > %d). PUL_REQUIRED", remoteTotal, localTotal)
		} else {
			// Rule 5: Totals equal but hash diff (Divergence)
			log.Printf("⚠️  Row counts equal but hashes differ. Using date tiebreaker.")
			localDate, _ := time.Parse(time.RFC3339, lastModified)
			if lastModified == "" {
				localDate = time.Unix(0, 0)
			}
			remoteDate, _ := time.Parse(time.RFC3339, remoteManifest.LastModified)

			if localDate.After(remoteDate) {
				log.Printf("🚀 Local is newer. Pushing...")
			} else if remoteDate.After(localDate) {
				return fmt.Errorf("remote is newer. PUL_REQUIRED")
			} else {
				return fmt.Errorf("CONFLICT DETECTED: Same size, same date, different data")
			}
		}
	}

	// 6. Create a consistent DB snapshot, calculate Hash and Record Count
	tmpSnapshot := s.dbPath + ".push_tmp"
	if err := s.createDBSnapshot(tmpSnapshot); err != nil {
		return fmt.Errorf("failed to create DB snapshot: %w", err)
	}
	defer os.Remove(tmpSnapshot)

	logicalHash, _ := s.db.CalculateLogicalHash()
	stats, _ := s.db.GetSyncStats()
	lastMod, _ := s.db.GetLastModified()

	// 7. Upload snapshot to Drive (Atomic via PATCH if possible)
	manifest := SyncManifest{
		LSN:          localLSN,
		LogicalHash:  logicalHash,
		LastModified: lastMod,
		Stats:        stats,
		PushedAt:     time.Now().Format(time.RFC3339),
	}

	if err := s.retryWithBackoff(3, "UploadDBFileToDrive", func(ctx context.Context) error {
		return s.UploadDBFileToDrive(ctx, manifest, tmpSnapshot)
	}); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}

	// 7.5 Post-Push Verification: Re-read from Drive and verify hash
	log.Printf("🧪 Verifying push integrity...")
	tempVerify := s.dbPath + ".verify_push"
	if err := s.DownloadFromDrive(tempVerify); err != nil {
		return fmt.Errorf("post-push verification download failed: %w", err)
	}
	defer os.Remove(tempVerify)

	verifyHash, err := s.calculateFileHash(tempVerify)
	if err != nil {
		return fmt.Errorf("post-push hash calculation failed: %w", err)
	}

	if verifyHash != logicalHash {
		log.Printf("❌ CRITICAL: Push corruption detected! Hash mismatch on Drive.")
		return fmt.Errorf("push verification failed: hash mismatch (local: %s, remote: %s)", logicalHash[:8], verifyHash[:8])
	}
	log.Printf("✅ Push verification successful.")

	// 8. Update Local Status — MUST SUCCEED
	if err := s.db.UpdatePushStatus(localLSN, logicalHash); err != nil {
		log.Printf("❌ CRITICAL: Failed to update local push status after successful push: %v", err)
		log.Printf("⚠️  WARNING: kv_store may be out of sync. Local manifest may not reflect remote push.")
		return fmt.Errorf("CRITICAL: post-push KV update failed (local<->remote sync broken): %w", err)
	}

	// 9. Clear Pending Sync
	s.db.ClearPendingSync()

	log.Printf("✅ DB successfully push to Drive (LSN %d, Hash %s)", localLSN, logicalHash[:8])
	return nil
}

// PullDBFromDrive implements the strict pull logic
func (s *SyncManager) PullDBFromDrive(force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.Printf("🔄 Starting strict DB pull from Drive...")

	// 1. Read Remote Manifest
	remoteManifest, err := s.GetRemoteManifest()
	if err != nil {
		return fmt.Errorf("failed to read remote manifest: %w", err)
	}
	if remoteManifest == nil {
		return fmt.Errorf("no remote backup found on Drive")
	}

	// 2. Local LSN Check (unless forced)
	if !force {
		localLSN, _ := s.db.GetLocalLSN()
		if remoteManifest.LSN <= localLSN {
			log.Printf("ℹ️  Local DB is newer or equal (Local: %d, Remote: %d). Pull skipped.", localLSN, remoteManifest.LSN)
			return nil
		}
	}

	// 3. Safety Check: Don't pull empty DB
	if remoteManifest.Stats["files"] == 0 && remoteManifest.Stats["folders"] == 0 {
		return fmt.Errorf("remote database is empty. Pull refused for safety")
	}

	// 4. Backup local DB before overwrite
	backupPath := s.dbPath + ".backup_pre_pull"
	if _, err := os.Stat(s.dbPath); err == nil {
		if err := s.copyFile(s.dbPath, backupPath); err != nil {
			log.Printf("⚠️  Local backup failed: %v", err)
		}
	}

	// 5. Download and Verify Logical Hash
	tempPath := s.dbPath + ".downloading"
	if err := s.DownloadFromDrive(tempPath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// To verify logical hash, we need to open the temp DB
	tempDB := &Database{}
	if err := tempDB.Init(tempPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("failed to open downloaded DB for verification: %w", err)
	}
	downloadedHash, err := tempDB.CalculateLogicalHash()
	tempDB.Close()

	if err != nil {
		os.Remove(tempPath)
		return err
	}

	if downloadedHash != remoteManifest.LogicalHash {
		os.Remove(tempPath)
		return fmt.Errorf("downloaded logical hash (%s) does not match manifest (%s). Corruption suspected", downloadedHash[:8], remoteManifest.LogicalHash[:8])
	}

	// 6. Final Integrity Check on Downloaded File
	if err := s.checkIntegrityOfFile(tempPath); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("downloaded DB integrity check failed: %w", err)
	}

	// 7. Atomic Replace
	s.db.Close() // Must close before move on Windows
	if err := os.Rename(tempPath, s.dbPath); err != nil {
		return fmt.Errorf("failed to replace local DB: %w", err)
	}

	// Re-open DB
	if err := s.db.Init(s.dbPath); err != nil {
		// This is bad, we might need to restore the backup
		log.Printf("❌ CRITICAL: Failed to re-open DB after pull: %v. Attempting restore...", err)
		os.Rename(backupPath, s.dbPath)
		s.db.Init(s.dbPath)
		return err
	}

	os.Remove(backupPath)
	log.Printf("✅ DB successfully pulled from Drive (LSN %d)", remoteManifest.LSN)
	return nil
}

func (s *SyncManager) GetRemoteManifest() (*SyncManifest, error) {
	// Use retry with backoff, but fail gracefully if no manifest exists yet (first backup)
	// Spec: Must read remote LSN before push, but first push has no manifest
	var lastErr error
	maxAttempts := 2

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		manifest, err := s.getRemoteManifestWithContext(ctx)
		cancel()

		if err == nil {
			return manifest, nil
		}

		// If manifest not found, this is the expected 'first push' case
		if isNotFoundError(err) {
			return nil, nil
		}

		// Record the last error and retry transient failures
		lastErr = err
		if attempt < maxAttempts {
			backoffDur := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			log.Printf("⚠️  GetRemoteManifest attempt %d failed: %v. Retrying in %v...", attempt, err, backoffDur)
			time.Sleep(backoffDur)
			continue
		}

		// Final attempt and it's a real error — return it
		return nil, lastErr
	}
	return nil, lastErr
}

func (s *SyncManager) getRemoteManifestWithContext(ctx context.Context) (*SyncManifest, error) {
	driveSvc := s.yt.GetDriveService()
	if driveSvc == nil {
		return nil, fmt.Errorf("drive service not available")
	}

	folderID, _ := s.pm.getRecoveryFolderID()
	query := "name = 'nexus-sync.json' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'nexus-sync.json' and '%s' in parents and trashed = false", folderID)
	}

	fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list files error: %w", err)
	}
	if len(fileList.Files) == 0 {
		return nil, &notFoundError{msg: "nexus-sync.json not found"}
	}

	resp, err := driveSvc.Files.Get(fileList.Files[0].Id).Context(ctx).Download()
	if err != nil {
		return nil, fmt.Errorf("download manifest error: %w", err)
	}
	defer resp.Body.Close()

	var manifest SyncManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode manifest error: %w", err)
	}
	return &manifest, nil
}

// Helper to detect "not found" errors (not retryable, just first backup)
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }
func isNotFoundError(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

func (s *SyncManager) CalculateDBHash() (string, error) {
	return s.calculateFileHash(s.dbPath)
}

// createDBSnapshot tries to produce a consistent snapshot of the DB at destPath.
// Preferred method is `VACUUM INTO` (atomic and consistent). If not supported
// fall back to checkpoint + file copy.
func (s *SyncManager) createDBSnapshot(destPath string) error {
	// Try VACUUM INTO first (requires recent SQLite)
	_, err := s.db.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", destPath))
	if err == nil {
		return nil
	}

	// Fallback: checkpoint WAL and copy file
	if cerr := s.db.Checkpoint(); cerr != nil {
		return fmt.Errorf("snapshot checkpoint failed: %w (vacuum err: %v)", cerr, err)
	}

	// Copy the DB file to destPath
	if copyErr := s.copyFile(s.dbPath, destPath); copyErr != nil {
		return fmt.Errorf("snapshot copy failed: %w (vacuum err: %v)", copyErr, err)
	}
	return nil
}

func (s *SyncManager) calculateFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *SyncManager) UploadToDrive(manifest SyncManifest) error {
	// Deprecated: prefer UploadDBFileToDrive with an explicit snapshot.
	// Create a consistent snapshot and upload it via the snapshot uploader.
	tmpSnapshot := s.dbPath + ".push_tmp"
	if err := s.createDBSnapshot(tmpSnapshot); err != nil {
		return fmt.Errorf("failed to create DB snapshot for upload: %w", err)
	}
	defer os.Remove(tmpSnapshot)

	// Use retry wrapper to provide context and backoff.
	if err := s.retryWithBackoff(3, "UploadToDrive", func(ctx context.Context) error {
		return s.UploadDBFileToDrive(ctx, manifest, tmpSnapshot)
	}); err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	return nil
}

// UploadDBFileToDrive uploads the provided DB file path as the nexus.db atomic
// upload and then uploads the manifest. This allows uploading a snapshot file
// rather than the live DB file.
func (s *SyncManager) UploadDBFileToDrive(ctx context.Context, manifest SyncManifest, dbFilePath string) error {
	driveSvc := s.yt.GetDriveService()
	if driveSvc == nil {
		return fmt.Errorf("drive service not available")
	}

	folderID, _ := s.pm.getRecoveryFolderID()

	// 1. Upload nexus.db (PATCH to preserve history)
	dbFile, err := os.Open(dbFilePath)
	if err != nil {
		return err
	}
	defer dbFile.Close()

	query := "name = 'nexus.db' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'nexus.db' and '%s' in parents and trashed = false", folderID)
	}

	fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").Context(ctx).Do()
	if err == nil && len(fileList.Files) > 0 {
		fileID := fileList.Files[0].Id
		log.Printf("📄 Found existing nexus.db (ID: %s), performing PATCH update...", fileID)
		if _, err = driveSvc.Files.Update(fileID, nil).Media(dbFile).Context(ctx).Do(); err != nil {
			return err
		}
	} else {
		log.Printf("📄 nexus.db not found on Drive, creating new file...")
		f := &drive.File{Name: "nexus.db", MimeType: "application/x-sqlite3"}
		if folderID != "" {
			f.Parents = []string{folderID}
		}
		if _, err = driveSvc.Files.Create(f).Media(dbFile).Context(ctx).Do(); err != nil {
			return err
		}
	}

	// 2. Upload Manifest
	manifestJSON, merr := json.MarshalIndent(manifest, "", "  ")
	if merr != nil {
		return fmt.Errorf("failed to marshal manifest JSON: %w", merr)
	}
	query = "name = 'nexus-sync.json' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'nexus-sync.json' and '%s' in parents and trashed = false", folderID)
	}
	fileList, err = driveSvc.Files.List().Q(query).Fields("files(id)").Context(ctx).Do()
	if err == nil && len(fileList.Files) > 0 {
		if _, err = driveSvc.Files.Update(fileList.Files[0].Id, nil).Media(bytes.NewReader(manifestJSON)).Context(ctx).Do(); err != nil {
			return err
		}
	} else {
		f := &drive.File{Name: "nexus-sync.json", MimeType: "application/json"}
		if folderID != "" {
			f.Parents = []string{folderID}
		}
		if _, err = driveSvc.Files.Create(f).Media(bytes.NewReader(manifestJSON)).Context(ctx).Do(); err != nil {
			return err
		}
	}

	return nil
}

func (s *SyncManager) DownloadFromDrive(destPath string) error {
	// Use retry with backoff for network resilience
	// Spec: 60s timeout per attempt, retry up to 3 times
	return s.retryWithBackoff(3, "DownloadFromDrive", func(ctx context.Context) error {
		driveSvc := s.yt.GetDriveService()
		if driveSvc == nil {
			return fmt.Errorf("drive service not available")
		}

		folderID, _ := s.pm.getRecoveryFolderID()
		query := "name = 'nexus.db' and trashed = false"
		if folderID != "" {
			query = fmt.Sprintf("name = 'nexus.db' and '%s' in parents and trashed = false", folderID)
		}

		// Find file with timeout
		fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").Context(ctx).Do()
		if err != nil || len(fileList.Files) == 0 {
			return fmt.Errorf("nexus.db not found on Drive")
		}

		// Download with timeout
		resp, err := driveSvc.Files.Get(fileList.Files[0].Id).Context(ctx).Download()
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
		defer resp.Body.Close()

		out, err := os.Create(destPath)
		if err != nil {
			return err
		}
		defer out.Close()

		_, err = io.Copy(out, resp.Body)
		return err
	})
}

func (s *SyncManager) copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func (s *SyncManager) checkIntegrityOfFile(path string) error {
	// Safety: Verify no shadow files exist on the temporary file
	// This should not happen in normal flow, but check for corruption
	shmPath := path + "-shm"
	walPath := path + "-wal"

	if _, err := os.Stat(shmPath); err == nil {
		return fmt.Errorf("shadow file .shm exists on downloaded temp file: %s (WAL corruption?)", shmPath)
	}
	if _, err := os.Stat(walPath); err == nil {
		return fmt.Errorf("shadow file .wal exists on downloaded temp file: %s (incomplete WAL?)", walPath)
	}

	tempDB, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer tempDB.Close()
	var res string
	err = tempDB.QueryRow("PRAGMA integrity_check").Scan(&res)
	if err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("integrity check failed: %s", res)
	}
	return nil
}
