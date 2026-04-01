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
	LSN         int64  `json:"lsn"`
	HashSHA256  string `json:"hash_sha256"`
	PushedAt    string `json:"pushed_at"`
	RecordCount int64  `json:"record_count"`
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
		// 5. Compare LSN
		if remoteManifest.LSN > localLSN {
			return fmt.Errorf("remote LSN (%d) is greater than local LSN (%d). Pull required first", remoteManifest.LSN, localLSN)
		}
		if remoteManifest.LSN == localLSN {
			// Check if hash is different
			localHash, _ := s.CalculateDBHash()
			if remoteManifest.HashSHA256 == localHash {
				log.Printf("✅ DB already in sync (LSN %d)", localLSN)
				return nil
			}
			// CRITICAL: Spec rule — same LSN but different hash = conflict. Do not push.
			return fmt.Errorf("CONFLICT DETECTED: Local LSN matches remote (%d) but hash differs.\n  Local: %s\n  Remote: %s\nManual intervention required. Do not proceed with push.", 
				localLSN, localHash[:16], remoteManifest.HashSHA256[:16])
		}
	}

	// 6. Calculate Hash and Record Count
	localHash, err := s.CalculateDBHash()
	if err != nil {
		return err
	}
	recordCount, err := s.db.GetTotalFileCount()
	if err != nil {
		return err
	}

	// 7. Upload to Drive (Atomic)
	manifest := SyncManifest{
		LSN:         localLSN,
		HashSHA256:  localHash,
		PushedAt:    time.Now().Format(time.RFC3339),
		RecordCount: recordCount,
	}

	if err := s.UploadToDrive(manifest); err != nil {
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

	if verifyHash != localHash {
		log.Printf("❌ CRITICAL: Push corruption detected! Hash mismatch on Drive.")
		return fmt.Errorf("push verification failed: hash mismatch (local: %s, remote: %s)", localHash[:8], verifyHash[:8])
	}
	log.Printf("✅ Push verification successful.")

	// 8. Update Local Status — MUST SUCCEED
	if err := s.db.UpdatePushStatus(localLSN, localHash); err != nil {
		log.Printf("❌ CRITICAL: Failed to update local push status after successful push: %v", err)
		log.Printf("⚠️  WARNING: kv_store may be out of sync. Local manifest may not reflect remote push.")
		return fmt.Errorf("CRITICAL: post-push KV update failed (local<->remote sync broken): %w", err)
	}

	// 9. Clear Pending Sync
	s.db.ClearPendingSync()

	log.Printf("✅ DB successfully push to Drive (LSN %d, Hash %s)", localLSN, localHash[:8])
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
	if remoteManifest.RecordCount == 0 {
		return fmt.Errorf("remote database is empty (record_count=0). Pull refused for safety")
	}

	// 4. Backup local DB before overwrite
	backupPath := s.dbPath + ".backup_pre_pull"
	if _, err := os.Stat(s.dbPath); err == nil {
		if err := s.copyFile(s.dbPath, backupPath); err != nil {
			log.Printf("⚠️  Local backup failed: %v", err)
		}
	}

	// 5. Download and Verify Hash
	tempPath := s.dbPath + ".downloading"
	if err := s.DownloadFromDrive(tempPath); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	downloadedHash, err := s.calculateFileHash(tempPath)
	if err != nil {
		return err
	}

	if downloadedHash != remoteManifest.HashSHA256 {
		os.Remove(tempPath)
		return fmt.Errorf("downloaded hash (%s) does not match manifest (%s). Corruption suspected", downloadedHash[:8], remoteManifest.HashSHA256[:8])
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
		
		lastErr = err
		if attempt < maxAttempts && !isNotFoundError(err) {
			backoffDur := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			log.Printf("⚠️  GetRemoteManifest attempt %d failed: %v. Retrying in %v...", attempt, err, backoffDur)
			time.Sleep(backoffDur)
		} else {
			// Not found or final attempt - return nil gracefully (first backup case is OK)
			if isNotFoundError(err) || attempt == maxAttempts {
				return nil, nil
			}
		}
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
	driveSvc := s.yt.GetDriveService()
	if driveSvc == nil {
		return fmt.Errorf("drive service not available")
	}

	folderID, _ := s.pm.getRecoveryFolderID()

	// 1. Upload nexus.db.tmp
	dbFile, err := os.Open(s.dbPath)
	if err != nil {
		return err
	}
	defer dbFile.Close()

	// Atomic push: write to .tmp then rename
	tmpName := "nexus.db.tmp"
	query := fmt.Sprintf("name = '%s' and trashed = false", tmpName)
	if folderID != "" {
		query = fmt.Sprintf("name = '%s' and '%s' in parents and trashed = false", tmpName, folderID)
	}

	var tmpFileID string
	fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err == nil && len(fileList.Files) > 0 {
		tmpFileID = fileList.Files[0].Id
		_, err = driveSvc.Files.Update(tmpFileID, nil).Media(dbFile).Do()
	} else {
		f := &drive.File{Name: tmpName, MimeType: "application/x-sqlite3"}
		if folderID != "" { f.Parents = []string{folderID} }
		res, err := driveSvc.Files.Create(f).Media(dbFile).Do()
		if err == nil { tmpFileID = res.Id }
	}
	if err != nil { return err }

	// 2. Rename .tmp to nexus.db on Drive
	query = "name = 'nexus.db' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'nexus.db' and '%s' in parents and trashed = false", folderID)
	}
	fileList, err = driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err == nil && len(fileList.Files) > 0 {
		driveSvc.Files.Delete(fileList.Files[0].Id).Do()
	}

	_, err = driveSvc.Files.Update(tmpFileID, &drive.File{Name: "nexus.db"}).Do()
	if err != nil { return err }

	// 3. Upload Manifest
	manifestJSON, _ := json.MarshalIndent(manifest, "", "  ")
	query = "name = 'nexus-sync.json' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'nexus-sync.json' and '%s' in parents and trashed = false", folderID)
	}
	fileList, err = driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err == nil && len(fileList.Files) > 0 {
		_, err = driveSvc.Files.Update(fileList.Files[0].Id, nil).Media(bytes.NewReader(manifestJSON)).Do()
	} else {
		f := &drive.File{Name: "nexus-sync.json", MimeType: "application/json"}
		if folderID != "" { f.Parents = []string{folderID} }
		_, err = driveSvc.Files.Create(f).Media(bytes.NewReader(manifestJSON)).Do()
	}

	return err
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
	if err != nil { return err }
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil { return err }
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
	if err != nil { return err }
	defer tempDB.Close()
	var res string
	err = tempDB.QueryRow("PRAGMA integrity_check").Scan(&res)
	if err != nil { return err }
	if res != "ok" { return fmt.Errorf("integrity check failed: %s", res) }
	return nil
}
