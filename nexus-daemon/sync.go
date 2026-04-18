package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"
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
	BinarySHA    string         `json:"binary_sha256,omitempty"`
	Encrypted    bool           `json:"encrypted,omitempty"`
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
func (s *SyncManager) PushDBToDrive() (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.Printf("🔄 Starting strict DB push to Drive...")

	// Circuit-breaker: refuse pushes if suspended
	if suspended, derr := s.isPushSuspended(); derr == nil && suspended {
		return fmt.Errorf("push suspended by circuit-breaker")
	} else if derr != nil {
		log.Printf("⚠️  Failed to evaluate push circuit-breaker: %v", derr)
	}

	// Track success/failure and update circuit-breaker state and structured logs
	start := time.Now()
	var logicalHash string
	var localLSN int64
	defer func() {
		// record circuit-breaker counters
		if err != nil {
			if rerr := s.recordPushFailure(); rerr != nil {
				log.Printf("⚠️  Failed to record push failure: %v", rerr)
			}
		} else {
			if rerr := s.resetPushFailures(); rerr != nil {
				log.Printf("⚠️  Failed to reset push failure counter: %v", rerr)
			}
		}

		// structured log entry
		entry := map[string]interface{}{
			"type":       "push_result",
			"lsn":        localLSN,
			"logicalHash": logicalHash,
			"success":    err == nil,
			"duration_s": time.Since(start).Seconds(),
		}
		if err != nil {
			entry["error"] = err.Error()
		}
		if lerr := s.appendSyncLog(entry); lerr != nil {
			log.Printf("⚠️ Failed to append sync log: %v", lerr)
		}
	}()

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
	localLSN, err = s.db.GetLocalLSN()
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
		// Explicit decision state machine with 5 cases:
		// 1) Identical -> noop
		// 2) Local richer -> push
		// 3) Remote richer -> require pull
		// 4) Equal counts but local newer -> push
		// 5) Equal counts but remote newer -> require pull

		statsLocal, _ := s.db.GetSyncStats()
		logicalLocal, _ := s.db.CalculateLogicalHash()
		lastLocal, _ := s.db.GetLastModified()

		statsRemote := remoteManifest.Stats
		logicalRemote := remoteManifest.LogicalHash
		lastRemote := remoteManifest.LastModified

		// Helper totals
		localTotal := statsLocal["files"] + statsLocal["folders"] + statsLocal["tasks"]
		remoteTotal := statsRemote["files"] + statsRemote["folders"] + statsRemote["tasks"]

		// Case 1: identical (counts + logical hash)
		if localTotal == remoteTotal && logicalLocal == logicalRemote {
			log.Printf("✅ DB already in sync (LSN %d)", localLSN)
			return nil
		}

		// Case 2/3: richer counts decide (local richer -> push, remote richer -> pull)
		if localTotal > remoteTotal {
			log.Printf("🚀 Decision: Local is richer (%d > %d). Proceeding to push.", localTotal, remoteTotal)
			// proceed
		} else if remoteTotal > localTotal {
			log.Printf("⏸️  Decision: Remote is richer (%d > %d). Pull required.", remoteTotal, localTotal)
			return fmt.Errorf("PUL_REQUIRED: remote richer (%d > %d)", remoteTotal, localTotal)
		} else {
			// Equal counts but different logical hashes -> tiebreak with lastModified
			// Parse times safely
			var localDate time.Time
			var remoteDate time.Time
			if lastLocal == "" {
				localDate = time.Unix(0, 0)
			} else {
				if d, e := time.Parse(time.RFC3339, lastLocal); e == nil {
					localDate = d
				} else {
					localDate = time.Unix(0, 0)
				}
			}
			if lastRemote == "" {
				remoteDate = time.Unix(0, 0)
			} else {
				if d, e := time.Parse(time.RFC3339, lastRemote); e == nil {
					remoteDate = d
				} else {
					remoteDate = time.Unix(0, 0)
				}
			}

			// If logical hashes match (should have matched earlier) treat as noop
			if logicalLocal == logicalRemote {
				log.Printf("✅ Logical hashes match after re-evaluation")
				return nil
			}

			if localDate.After(remoteDate) {
				log.Printf("🚀 Decision: counts equal but local is newer by timestamp. Proceeding to push.")
				// proceed
			} else if remoteDate.After(localDate) {
				log.Printf("⏸️  Decision: counts equal but remote is newer by timestamp. Pull required.")
				return fmt.Errorf("PUL_REQUIRED: remote newer by timestamp")
			} else {
				// Exact tie on counts and timestamps but different logical hashes -> conflict
				log.Printf("❗ CONFLICT_DETECTED: equal counts & dates but different logical hashes")
				return fmt.Errorf("CONFLICT_DETECTED: equal counts & dates but different logical hashes")
			}
		}
	}

	// 6. Create a consistent DB snapshot, calculate Hash and Record Count
	tmpSnapshot := s.dbPath + ".push_tmp"
	if err := s.createDBSnapshot(tmpSnapshot); err != nil {
		return fmt.Errorf("failed to create DB snapshot: %w", err)
	}
	defer os.Remove(tmpSnapshot)

	// compute binary SHA for the snapshot as additional verification metadata
	snapshotBinarySHA, err := s.calculateFileHash(tmpSnapshot)
	if err != nil {
		return fmt.Errorf("failed to compute snapshot binary hash: %w", err)
	}

	logicalHash, _ = s.db.CalculateLogicalHash()
	stats, _ := s.db.GetSyncStats()
	lastMod, _ := s.db.GetLastModified()

	// 7. Upload snapshot to Drive (Atomic via PATCH if possible)
	manifest := SyncManifest{
		LSN:          localLSN,
		LogicalHash:  logicalHash,
		LastModified: lastMod,
		Stats:        stats,
		PushedAt:     time.Now().Format(time.RFC3339),
		BinarySHA:    snapshotBinarySHA,
	}

	// structured log: push start
	if lerr := s.appendSyncLog(map[string]interface{}{"type": "push_start", "lsn": localLSN, "time": time.Now().Format(time.RFC3339)}); lerr != nil {
		log.Printf("⚠️ Failed to append push_start log: %v", lerr)
	}

	// If a master key is provided via NEXUS_SNAPSHOT_KEY (hex, 32 bytes), encrypt the snapshot before upload.
	if key, kerr := readMasterKey(); kerr == nil && key != nil {
		encPath := tmpSnapshot + ".enc"
		log.Printf("🔒 Master key present: encrypting snapshot to %s before upload", encPath)
		if err := encryptFileAESGCM(tmpSnapshot, encPath, key); err != nil {
			return fmt.Errorf("failed to encrypt snapshot before upload: %w", err)
		}
		// upload encrypted file but keep BinarySHA as hash of plaintext
		manifest.Encrypted = true
		if err := s.retryWithBackoff(3, "UploadDBFileToDrive", func(ctx context.Context) error {
			return s.UploadDBFileToDrive(ctx, manifest, encPath)
		}); err != nil {
			os.Remove(encPath)
			return fmt.Errorf("upload failed: %w", err)
		}
		os.Remove(encPath)
	} else if kerr != nil {
		return fmt.Errorf("invalid master key: %w", kerr)
	} else {
		// No master key: upload plaintext snapshot (existing behavior)
		if err := s.retryWithBackoff(3, "UploadDBFileToDrive", func(ctx context.Context) error {
			return s.UploadDBFileToDrive(ctx, manifest, tmpSnapshot)
		}); err != nil {
			return fmt.Errorf("upload failed: %w", err)
		}
	}

	// 7.5 Post-Push Verification: Re-read from Drive and verify hash
	log.Printf("🧪 Verifying push integrity...")
	tempVerify := s.dbPath + ".verify_push"
	if err := s.DownloadFromDrive(tempVerify); err != nil {
		return fmt.Errorf("post-push verification download failed: %w", err)
	}
	defer os.Remove(tempVerify)

	// If snapshot was uploaded encrypted, decrypt the downloaded file before verification
	verifyPath := tempVerify
	if manifest.Encrypted {
		key, kerr := readMasterKey()
		if kerr != nil {
			os.Remove(tempVerify)
			return fmt.Errorf("master key invalid or missing for post-push verification: %w", kerr)
		}
		decPath := tempVerify + ".dec"
		if err := decryptFileAESGCM(tempVerify, decPath, key); err != nil {
			os.Remove(tempVerify)
			return fmt.Errorf("failed to decrypt downloaded snapshot for verification: %w", err)
		}
		// remove the encrypted temp and use decrypted path for checks
		os.Remove(tempVerify)
		verifyPath = decPath
		defer os.Remove(decPath)
	}

	// 1) Optional: verify binary SHA if manifest contains it (plaintext SHA)
	if manifest.BinarySHA != "" {
		downloadedBinarySHA, err := s.calculateFileHash(verifyPath)
		if err != nil {
			os.Remove(verifyPath)
			return fmt.Errorf("post-push binary hash calculation failed: %w", err)
		}
		if downloadedBinarySHA != manifest.BinarySHA {
			log.Printf("❌ CRITICAL: Push corruption detected! Binary SHA mismatch on Drive.")
			os.Remove(verifyPath)
			return fmt.Errorf("push verification failed: binary sha mismatch (manifest: %s, remote: %s)", manifest.BinarySHA[:8], downloadedBinarySHA[:8])
		}
		log.Printf("✅ Post-push binary SHA matched manifest.")
	}

	// 2) Verify by opening the downloaded snapshot as a DB and comparing logical hashes
	tempDB := &Database{}
	if err := tempDB.Init(verifyPath); err != nil {
		os.Remove(verifyPath)
		return fmt.Errorf("post-push verification failed to open downloaded DB: %w", err)
	}
	downloadedLogicalHash, err := tempDB.CalculateLogicalHash()
	tempDB.Close()
	if err != nil {
		os.Remove(verifyPath)
		return fmt.Errorf("post-push logical hash calculation failed: %w", err)
	}

	if downloadedLogicalHash != logicalHash {
		log.Printf("❌ CRITICAL: Push corruption detected! Logical hash mismatch on Drive.")
		os.Remove(verifyPath)
		return fmt.Errorf("push verification failed: logical hash mismatch (local: %s, remote: %s)", logicalHash[:8], downloadedLogicalHash[:8])
	}
	log.Printf("✅ Push verification successful (logical hash matched).")

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
func (s *SyncManager) PullDBFromDrive(force bool) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	log.Printf("🔄 Starting strict DB pull from Drive...")

	start := time.Now()
	var remoteLSN int64
	var remoteLogical string
	defer func() {
		entry := map[string]interface{}{
			"type":      "pull_result",
			"lsn":       remoteLSN,
			"success":   err == nil,
			"duration_s": time.Since(start).Seconds(),
		}
		if remoteLogical != "" {
			entry["logicalHash"] = remoteLogical
		}
		if err != nil {
			entry["error"] = err.Error()
		}
		if lerr := s.appendSyncLog(entry); lerr != nil {
			log.Printf("⚠️ Failed to append pull_result log: %v", lerr)
		}
	}()

	// 1. Read Remote Manifest
	remoteManifest, err := s.GetRemoteManifest()
	if err != nil {
		return fmt.Errorf("failed to read remote manifest: %w", err)
	}
	if remoteManifest == nil {
		return fmt.Errorf("no remote backup found on Drive")
	}
	remoteLSN = remoteManifest.LSN
	remoteLogical = remoteManifest.LogicalHash

	// structured log: pull start
	if lerr := s.appendSyncLog(map[string]interface{}{"type": "pull_start", "lsn": remoteLSN, "time": time.Now().Format(time.RFC3339)}); lerr != nil {
		log.Printf("⚠️ Failed to append pull_start log: %v", lerr)
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

	// If the remote snapshot is encrypted, decrypt it before verification/replace
	var verifyPath string = tempPath
	if remoteManifest.Encrypted {
		key, kerr := readMasterKey()
		if kerr != nil {
			os.Remove(tempPath)
			return fmt.Errorf("master key invalid or missing for decrypting snapshot: %w", kerr)
		}
		decPath := tempPath + ".dec"
		if err := decryptFileAESGCM(tempPath, decPath, key); err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("failed to decrypt downloaded snapshot: %w", err)
		}
		// prefer to remove the encrypted temp after successful decryption
		os.Remove(tempPath)
		verifyPath = decPath
		defer os.Remove(decPath)
	}

	// To verify logical hash, we need to open the (possibly decrypted) temp DB
	tempDB := &Database{}
	if err := tempDB.Init(verifyPath); err != nil {
		os.Remove(verifyPath)
		return fmt.Errorf("failed to open downloaded DB for verification: %w", err)
	}
	downloadedHash, err := tempDB.CalculateLogicalHash()
	tempDB.Close()

	if err != nil {
		os.Remove(verifyPath)
		return err
	}

	if downloadedHash != remoteManifest.LogicalHash {
		os.Remove(verifyPath)
		return fmt.Errorf("downloaded logical hash (%s) does not match manifest (%s). Corruption suspected", downloadedHash[:8], remoteManifest.LogicalHash[:8])
	}

	// 6. Final Integrity Check on (possibly decrypted) file
	if err := s.checkIntegrityOfFile(verifyPath); err != nil {
		os.Remove(verifyPath)
		return fmt.Errorf("downloaded DB integrity check failed: %w", err)
	}

	// 7. Atomic Replace
	s.db.Close() // Must close before move on Windows

	// Move the downloaded temp file into place
	if err := os.Rename(tempPath, s.dbPath); err != nil {
		return fmt.Errorf("failed to replace local DB: %w", err)
	}

	// Re-open DB. If Init fails, attempt automatic rollback from backup_pre_pull.
	if err := s.db.Init(s.dbPath); err != nil {
		log.Printf("❌ CRITICAL: Failed to re-open DB after pull: %v. Attempting automatic restore from backup_pre_pull...", err)

		// If backup exists, try to restore it atomically
		if _, statErr := os.Stat(backupPath); statErr == nil {
			// Remove the faulty DB file before restoring
			if rerr := os.Remove(s.dbPath); rerr != nil {
				log.Printf("⚠️  Failed to remove faulty DB at %s before restore: %v", s.dbPath, rerr)
			}

			if rerr := os.Rename(backupPath, s.dbPath); rerr != nil {
				log.Printf("❌ Failed to restore backup_pre_pull (%s -> %s): %v", backupPath, s.dbPath, rerr)
				return fmt.Errorf("pull failed and automatic restore failed: %w; restore error: %v", err, rerr)
			}

			// Try to re-open restored DB
			if ierr := s.db.Init(s.dbPath); ierr != nil {
				log.Printf("❌ CRITICAL: Restored DB failed to open after restore: %v", ierr)
				return fmt.Errorf("pull failed, restore attempted but restored DB invalid: %w; restore-open-error: %v", err, ierr)
			}

			log.Printf("✅ Automatic restore from backup_pre_pull succeeded. Local DB restored.")
			return fmt.Errorf("pull failed initially but automatic restore succeeded; original error: %w", err)
		}

		// No backup available or stat failed
		log.Printf("❌ No backup_pre_pull available to restore. Local DB may be left in a corrupted state.")
		return fmt.Errorf("pull failed and no backup available to restore: %w", err)
	}

	// Success: remove the backup
	if err := os.Remove(backupPath); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️  Failed to remove backup_pre_pull (%s): %v", backupPath, err)
	}

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

// appendSyncLog stores a structured sync event into kv_store with an incrementing key
func (s *SyncManager) appendSyncLog(entry map[string]interface{}) error {
	// get counter
	raw, _ := s.db.GetKV("sync_log_counter")
	n := 0
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			n = v
		}
	}
	n++
	key := fmt.Sprintf("sync_log_%d", n)
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := s.db.SetKV(key, string(data)); err != nil {
		return err
	}
	// update counter
	if err := s.db.SetKV("sync_log_counter", strconv.Itoa(n)); err != nil {
		return err
	}

	// Rotation: keep only the most recent N entries to avoid unbounded growth
	const maxLogs = 1000
	if n > maxLogs {
		// delete oldest entries from 1..(n-maxLogs)
		cutoff := n - maxLogs
		for i := 1; i <= cutoff; i++ {
			delKey := fmt.Sprintf("sync_log_%d", i)
			// best-effort delete
			_ = s.db.SetKV(delKey, "")
		}
		// Note: we don't renumber existing keys; counter continues increasing.
	}
	return nil
}

// Circuit breaker helpers: store counters in kv_store
func (s *SyncManager) isPushSuspended() (bool, error) {
	val, ok := s.db.GetKV("push_suspended_until")
	if !ok || val == "" {
		return false, nil
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return false, err
	}
	if time.Now().Before(t) {
		return true, nil
	}
	// expired -> clear
	if err := s.db.SetKV("push_suspended_until", ""); err != nil {
		return false, err
	}
	return false, nil
}

func (s *SyncManager) recordPushFailure() error {
	raw, _ := s.db.GetKV("push_fail_count")
	n := 0
	if raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			n = v
		}
	}
	n++
	if err := s.db.SetKV("push_fail_count", strconv.Itoa(n)); err != nil {
		return err
	}
	// Threshold -> suspend pushes for a cooldown
	const threshold = 3
	const cooldown = 15 * time.Minute
	if n >= threshold {
		until := time.Now().Add(cooldown).Format(time.RFC3339)
		if err := s.db.SetKV("push_suspended_until", until); err != nil {
			return err
		}
		log.Printf("⛔ Circuit-breaker: push suspended until %s after %d consecutive failures", until, n)
	}
	return nil
}

func (s *SyncManager) resetPushFailures() error {
	if err := s.db.SetKV("push_fail_count", "0"); err != nil {
		return err
	}
	if err := s.db.SetKV("push_suspended_until", ""); err != nil {
		return err
	}
	return nil
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

	// Prefer the most-recent manifest if duplicates exist. Include metadata for dedupe auditing.
	fileList, err := driveSvc.Files.List().Q(query).OrderBy("modifiedTime desc").Fields("files(id,modifiedTime,md5Checksum)").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list files error: %w", err)
	}
	if len(fileList.Files) == 0 {
		return nil, &notFoundError{msg: "nexus-sync.json not found"}
	}
	if len(fileList.Files) > 1 {
		log.Printf("⚠️  Multiple manifest files found (%d). Keeping most-recent (ID: %s). Archiving older duplicates.", len(fileList.Files), fileList.Files[0].Id)
		// Archive older duplicates (keep index 0)
		for i := 1; i < len(fileList.Files); i++ {
			dupID := fileList.Files[i].Id
			archiveName := fmt.Sprintf("nexus-sync.json.dup.%s.%s", time.Now().UTC().Format("20060102T150405Z"), dupID[:8])
			copyMeta := &drive.File{Name: archiveName}
			if folderID != "" {
				copyMeta.Parents = []string{folderID}
			}
			if _, cerr := driveSvc.Files.Copy(dupID, copyMeta).Context(ctx).Do(); cerr != nil {
				log.Printf("⚠️ failed to archive duplicate manifest %s: %v", dupID, cerr)
				continue
			}
			if derr := driveSvc.Files.Delete(dupID).Context(ctx).Do(); derr != nil {
				log.Printf("⚠️ failed to delete duplicate manifest %s after archiving: %v", dupID, derr)
			} else {
				log.Printf("🗄️ Archived and removed duplicate manifest %s -> %s", dupID, archiveName)
			}
		}
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

	// Ensure WAL is empty before copying. If not, attempt a second TRUNCATE checkpoint.
	walPath := s.dbPath + "-wal"
	if info, statErr := os.Stat(walPath); statErr == nil && info.Size() > 0 {
		log.Printf("⚠️  WAL file has %d bytes after first checkpoint. Attempting TRUNCATE checkpoint.", info.Size())
		if cerr2 := s.db.Checkpoint(); cerr2 != nil {
			return fmt.Errorf("snapshot checkpoint truncate failed: %w", cerr2)
		}
		// re-stat
		if info2, statErr2 := os.Stat(walPath); statErr2 == nil && info2.Size() > 0 {
			return fmt.Errorf("snapshot aborted: WAL still non-empty (%d bytes) after checkpoint; refusing to copy", info2.Size())
		}
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

// readMasterKey reads hex key from env NEXUS_SNAPSHOT_KEY (32 bytes hex => 32 bytes key)
func readMasterKey() ([]byte, error) {
	hexk := os.Getenv("NEXUS_SNAPSHOT_KEY")
	if hexk == "" {
		return nil, nil
	}
	b, err := hex.DecodeString(hexk)
	if err != nil {
		return nil, fmt.Errorf("invalid NEXUS_SNAPSHOT_KEY hex: %w", err)
	}
	if len(b) != 32 {
		return nil, fmt.Errorf("invalid NEXUS_SNAPSHOT_KEY length: want 32 bytes, got %d", len(b))
	}
	return b, nil
}

// encryptFileAESGCM encrypts src -> dst using AES-256-GCM. Output format: 12-byte nonce || ciphertext
func encryptFileAESGCM(src, dst string, key []byte) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	out := g.Seal(nil, nonce, in, nil)
	// prepend nonce
	data := append(nonce, out...)
	return os.WriteFile(dst, data, 0600)
}

// decryptFileAESGCM decrypts src -> dst assuming format: 12-byte nonce || ciphertext
func decryptFileAESGCM(src, dst string, key []byte) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	ns := g.NonceSize()
	if len(data) < ns {
		return fmt.Errorf("ciphertext too short")
	}
	nonce := data[:ns]
	cipherText := data[ns:]
	plain, err := g.Open(nil, nonce, cipherText, nil)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, plain, 0600)
}

	// use crypto/rand.Reader for nonce generation

func (s *SyncManager) UploadToDrive(manifest SyncManifest) error {
	// Deprecated: prefer UploadDBFileToDrive with an explicit snapshot.
	// Create a consistent snapshot and upload it via the snapshot uploader.
	tmpSnapshot := s.dbPath + ".push_tmp"
	if err := s.createDBSnapshot(tmpSnapshot); err != nil {
		return fmt.Errorf("failed to create DB snapshot for upload: %w", err)
	}
	defer os.Remove(tmpSnapshot)

	// If master key present, encrypt snapshot before uploading
	if key, kerr := readMasterKey(); kerr == nil && key != nil {
		encPath := tmpSnapshot + ".enc"
		if err := encryptFileAESGCM(tmpSnapshot, encPath, key); err != nil {
			return fmt.Errorf("failed to encrypt snapshot for upload: %w", err)
		}
		manifest.Encrypted = true
		if err := s.retryWithBackoff(3, "UploadToDrive", func(ctx context.Context) error {
			return s.UploadDBFileToDrive(ctx, manifest, encPath)
		}); err != nil {
			os.Remove(encPath)
			return fmt.Errorf("upload failed: %w", err)
		}
		os.Remove(encPath)
		return nil
	} else if kerr != nil {
		return fmt.Errorf("invalid master key: %w", kerr)
	}

	// Use retry wrapper to provide context and backoff for plaintext upload.
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

	// Prefer the most-recent nexus.db file and include metadata to detect duplicates
	fileList, err := driveSvc.Files.List().Q(query).OrderBy("modifiedTime desc").Fields("files(id,modifiedTime,md5Checksum,parents)").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("drive list error: %w", err)
	}

	if len(fileList.Files) > 0 {
		// If multiple, remove older duplicates (keep the most recent at index 0)
		if len(fileList.Files) > 1 {
			log.Printf("⚠️  Multiple nexus.db files found on Drive (%d). Keeping most recent (ID: %s), deleting older duplicates.", len(fileList.Files), fileList.Files[0].Id)
			for i := 1; i < len(fileList.Files); i++ {
				did := fileList.Files[i].Id
				if err := driveSvc.Files.Delete(did).Context(ctx).Do(); err != nil {
					log.Printf("⚠️  Failed to delete duplicate nexus.db (ID: %s): %v", did, err)
				} else {
					log.Printf("🗑️  Deleted old duplicate nexus.db (ID: %s)", did)
				}
			}
		}

		// Create a backup copy of the current nexus.db on Drive before updating
		existingID := fileList.Files[0].Id
		backupName := fmt.Sprintf("nexus.db.bak.%s", time.Now().Format("20060102T150405"))
		backupFile := &drive.File{Name: backupName}
		if folderID != "" {
			backupFile.Parents = []string{folderID}
		}
		if _, err := driveSvc.Files.Copy(existingID, backupFile).Context(ctx).Do(); err != nil {
			log.Printf("⚠️  Failed to create Drive backup of existing nexus.db (ID: %s): %v", existingID, err)
		} else {
			log.Printf("💾 Created Drive backup of nexus.db as %s", backupName)
		}

		// Update the existing file (PATCH)
		fileID := existingID
		log.Printf("📄 Updating existing nexus.db (ID: %s) via PATCH...", fileID)
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
	fileList, err = driveSvc.Files.List().Q(query).OrderBy("modifiedTime desc").Fields("files(id,modifiedTime,md5Checksum)").Context(ctx).Do()
	if err == nil && len(fileList.Files) > 0 {
		if len(fileList.Files) > 1 {
			log.Printf("⚠️  Multiple manifest files found when uploading (%d). Updating most-recent (ID: %s).", len(fileList.Files), fileList.Files[0].Id)
		}
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
		// Prefer the most-recent nexus.db file and include metadata to detect duplicates
		fileList, err := driveSvc.Files.List().Q(query).OrderBy("modifiedTime desc").Fields("files(id,modifiedTime,md5Checksum)").Context(ctx).Do()
		if err != nil || len(fileList.Files) == 0 {
			return fmt.Errorf("nexus.db not found on Drive")
		}
		if len(fileList.Files) > 1 {
			log.Printf("⚠️  Multiple nexus.db files found on Drive (%d). Using most-recent (ID: %s).", len(fileList.Files), fileList.Files[0].Id)
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
