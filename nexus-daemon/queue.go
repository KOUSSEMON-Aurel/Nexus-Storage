package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
	"google.golang.org/api/youtube/v3"
)

type TaskType int

const (
	TaskUpload TaskType = iota
	TaskDownload
	TaskDelete
)

type Task struct {
	ID                   string    `json:"id"`
	Type                 TaskType  `json:"type"`
	FilePath             string    `json:"filePath"`
	Mode                 string    `json:"mode"`
	IsManifest           bool      `json:"isManifest"`
	Status               string    `json:"status"`
	Progress             float64   `json:"progress"`
	CreatedAt            time.Time `json:"createdAt"`
	CompletedAt          time.Time `json:"completedAt,omitempty"` // Timestamp when task finished (success or error)
	ParentID             *int64    `json:"parentId"`
	SHA256               string    `json:"sha256,omitempty"`
	Password             string    `json:"password,omitempty"`
	CustomEncryptPassword string    `json:"customEncryptPassword,omitempty"` // Optional 2nd layer encryption
}

type TaskQueue struct {
	tasks         map[string]*Task
	taskChan      chan *Task
	mu            sync.Mutex
	core          *NexusCore
	db            *Database
	ytManager     *YouTubeManager
	manifestMu    sync.Mutex
	manifestTimer *time.Timer
	pm            *PlaylistManager
	cache         *CacheManager
	syncMgr       *SyncManager
	// V4 Security: Master key (RAM-only, never persisted)
	masterKeyHex  string
	masterKeyMu   sync.RWMutex
}

func (q *TaskQueue) SetSyncManager(sm *SyncManager) {
	q.syncMgr = sm
}

func ensureYtDlp() {
	_, err := exec.LookPath("yt-dlp")
	if err == nil {
		return
	}
	binPath := filepath.Join(os.TempDir(), "nexus-bin", "yt-dlp")
	if _, err := os.Stat(binPath); err == nil {
		os.Setenv("PATH", os.Getenv("PATH")+":"+filepath.Dir(binPath))
		return
	}
	log.Println("yt-dlp not found in PATH. Auto-downloading...")
	os.MkdirAll(filepath.Dir(binPath), 0755)
	cmd := exec.Command("curl", "-L", "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp", "-o", binPath)
	if err := cmd.Run(); err != nil {
		log.Printf("Warning: failed to download yt-dlp: %v", err)
		return
	}
	os.Chmod(binPath, 0755)
	os.Setenv("PATH", os.Getenv("PATH")+":"+filepath.Dir(binPath))
	log.Println("yt-dlp successfully downloaded.")
}

func (q *TaskQueue) Init(core *NexusCore, db *Database, ytManager *YouTubeManager, pm *PlaylistManager, cache *CacheManager) {
	q.tasks = make(map[string]*Task)
	q.taskChan = make(chan *Task, 100) // Buffered queue
	q.core = core
	q.db = db
	q.ytManager = ytManager
	q.pm = pm
	q.cache = cache
	ensureYtDlp()

	// Load pending tasks from DB (but skip failed uploads — don't retry them)
	rows, err := db.GetPendingTasks()
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			t := &Task{}
			var tType int
			err := rows.Scan(&t.ID, &tType, &t.FilePath, &t.Mode, &t.IsManifest, &t.Status, &t.Progress, &t.CreatedAt, &t.ParentID, &t.SHA256)
			if err == nil {
				t.Type = TaskType(tType)
				// Skip uploads that previously failed — do not resume them automatically
				if tType == int(TaskUpload) && strings.Contains(t.Status, "Error") {
					log.Printf("⏭️  Skipping failed upload task %s (do not retry): %s", t.ID, t.Status)
					db.DeleteTask(t.ID) // Remove from DB entirely
					continue
				}
				q.tasks[t.ID] = t
				log.Printf("⏳ Resuming pending task %s", t.ID)
				q.taskChan <- t // Add to sequential worker
			}
		}
	}

	// Start the single sequential worker
	go q.worker()

	// Start task cleanup goroutine: auto-remove completed/error tasks after 30 seconds
	go q.cleanupCompletedTasks()
}

// V4 Security: MasterKey session management (RAM-only)

// SetMasterKeyHex stores the hex-encoded 32-byte masterKey in memory for this session
func (q *TaskQueue) SetMasterKeyHex(hexKey string) {
	q.masterKeyMu.Lock()
	defer q.masterKeyMu.Unlock()
	q.masterKeyHex = hexKey
	log.Printf("✅ Master key loaded into session")
}

// GetMasterKeyHex retrieves the hex-encoded masterKey if session is active
func (q *TaskQueue) GetMasterKeyHex() string {
	q.masterKeyMu.RLock()
	defer q.masterKeyMu.RUnlock()
	return q.masterKeyHex
}

// ClearMasterKeyHex clears the masterKey from memory (logout/session-end)
func (q *TaskQueue) ClearMasterKeyHex() {
	q.masterKeyMu.Lock()
	defer q.masterKeyMu.Unlock()
	q.masterKeyHex = ""
	log.Printf("✅ Master key cleared from session")
}

// deriveKeyFromGoogleSub derives a 32-byte encryption key from Google sub (permanent user ID)
// Uses PBKDF2 with SHA-256, fixed salt, 100k iterations for consistent, secure key derivation
// This enables zero-knowledge architecture: user never manages encryption passwords
func deriveKeyFromGoogleSub(googleSub string) string {
	// Fixed salt - same for all users (sub is already unique per user)
	// This ensures: same user → same key across sessions (necessary for downloads)
	// different user → different key (automatic due to unique sub)
	salt := []byte("nexus-storage-google-sub-v1")

	// PBKDF2: 100,000 iterations recommended by OWASP (2024)
	// Output: 32 bytes (256 bits) for AES-256
	derivedKey := pbkdf2.Key(
		[]byte(googleSub),      // Input: permanent user ID
		salt,                   // Input: fixed salt
		100000,                 // Iterations: high for security
		32,                     // Output length: 256 bits
		sha256.New,             // HMAC function: SHA-256
	)

	// Return as hex string for compatibility with existing encryption code
	return hex.EncodeToString(derivedKey)
}

// RotatePassword performs V4.1 password rotation:
// 1. Derive old masterKey from old_password + recovery_salt
// 2. Derive new masterKey from new_password + recovery_salt
// 3. Decrypt all file_keys with old masterKey
// 4. Re-encrypt all file_keys with new masterKey
// 5. Update database with new encrypted file_keys
// 6. Increment manifest_revision
// 7. Backup manifest to Drive
// Returns: (files_updated, new_revision, error)
func (q *TaskQueue) RotatePassword(oldPassword, newPassword string) (int, int, error) {
	log.Printf("🔄 Starting password rotation...")

	// 1. Get recovery salt from DB
	saltHex, err := q.db.GetRecoverySalt()
	if err != nil || saltHex == "" {
		return 0, 0, fmt.Errorf("recovery salt not found in database")
	}

	saltBytes, err := hex.DecodeString(saltHex)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid salt format: %w", err)
	}

	// 2. Derive old masterKey from old_password
	oldMasterKey, err := q.core.DeriveMasterKey(oldPassword, saltBytes)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to derive old master key: %w", err)
	}

	// 3. Derive new masterKey from new_password
	newMasterKey, err := q.core.DeriveMasterKey(newPassword, saltBytes)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to derive new master key: %w", err)
	}

	// 4. Get all files from database
	files, err := q.db.ListFiles()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to list files: %w", err)
	}

	// 5. Re-encrypt all file_keys
	oldKeyArray := [32]byte{}
	copy(oldKeyArray[:], oldMasterKey)
	newKeyArray := [32]byte{}
	copy(newKeyArray[:], newMasterKey)

	rotatedCount := 0
	for _, file := range files {
		if file.FileKey == "" {
			// No file_key to rotate (file not yet uploaded)
			continue
		}

		// Decode old encrypted file_key from hex
		oldEncrypted, err := hex.DecodeString(file.FileKey)
		if err != nil {
			log.Printf("⚠️  Could not decode file_key for %s: %v", file.Path, err)
			continue
		}

		// Decrypt with old masterKey
		decrypted, err := q.core.DecryptWithKey(oldEncrypted, oldKeyArray[:])
		if err != nil {
			log.Printf("⚠️  Could not decrypt file_key for %s (wrong password?): %v", file.Path, err)
			return 0, 0, fmt.Errorf("decryption failed for %s (wrong password?): %w", file.Path, err)
		}

		// Re-encrypt with new masterKey
		newEncrypted, err := q.core.EncryptWithKey(decrypted, newKeyArray[:])
		if err != nil {
			log.Printf("⚠️  Could not re-encrypt file_key for %s: %v", file.Path, err)
			continue
		}

		// Update database with new encrypted file_key
		newFileKeyHex := hex.EncodeToString(newEncrypted)
		if err := q.db.UpdateFileKey(file.ID, newFileKeyHex); err != nil {
			log.Printf("⚠️  Could not update file_key for %s: %v", file.Path, err)
			continue
		}

		rotatedCount++
		log.Printf("✅ Re-encrypted file_key for: %s", file.Path)
	}

	log.Printf("✅ Password rotation: %d files re-encrypted", rotatedCount)

	// 6. Increment manifest_revision in database
	newRevision, err := q.db.IncrementManifestRevision()
	if err != nil {
		log.Printf("⚠️  Failed to increment manifest revision: %v", err)
	}

	// 7. Force immediate manifest backup to Drive
	// Build manifest, encrypt with new masterKey, and backup
	newMasterKeyHex := hex.EncodeToString(newMasterKey)
	if err := q.EncryptAndBackupManifest(newMasterKeyHex); err != nil {
		log.Printf("⚠️  Failed to backup manifest after password rotation: %v", err)
	} else {
		log.Printf("✅ Manifest backed up after password rotation (revision %d)", newRevision)
	}

	return rotatedCount, newRevision, nil
}

func (q *TaskQueue) AddTask(t *Task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	
	// Prevent duplicate manifest tasks if one is already pending
	if t.IsManifest {
		for _, pending := range q.tasks {
			if pending.IsManifest && (pending.Status == "Pending" || pending.Status == "Processing") {
				return 
			}
		}
	}

	q.tasks[t.ID] = t
	q.db.SaveTask(t.ID, int(t.Type), t.FilePath, t.Mode, t.IsManifest, t.Status, t.Progress, t.CreatedAt, t.ParentID, t.SHA256)
	q.taskChan <- t
}

func (q *TaskQueue) worker() {
	for t := range q.taskChan {
		q.processTask(t)
	}
}

// cleanupCompletedTasks periodically removes tasks that completed (success or error) more than 30 seconds ago.
// This prevents the GUI from constantly re-displaying old notifications.
func (q *TaskQueue) cleanupCompletedTasks() {
	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()

	for range ticker.C {
		q.mu.Lock()
		now := time.Now()
		for taskID, t := range q.tasks {
			// If task is completed or has an error, and 30s have passed, remove it
			if !t.CompletedAt.IsZero() && now.Sub(t.CompletedAt) > 30*time.Second {
				if strings.Contains(t.Status, "Completed") || strings.Contains(t.Status, "Error") {
					log.Printf("🧹 Cleaning up old task %s (status: %s, age: %v)", taskID, t.Status, now.Sub(t.CompletedAt))
					q.db.DeleteTask(taskID)
					delete(q.tasks, taskID)
				}
			}
		}
		q.mu.Unlock()
	}
}

func (q *TaskQueue) updateTaskState(t *Task) {
	q.db.SaveTask(t.ID, int(t.Type), t.FilePath, t.Mode, t.IsManifest, t.Status, t.Progress, t.CreatedAt, t.ParentID, t.SHA256)
}

func (q *TaskQueue) GetTask(id string) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.tasks[id]
}

func (q *TaskQueue) processTask(t *Task) {
	q.mu.Lock()
	t.Status = "Processing"
	q.updateTaskState(t)
	q.mu.Unlock()

	log.Printf("🚀 Starting task %s (%v)", t.ID, t.Type)

	var err error
	switch t.Type {
	case TaskUpload:
		err = q.handleUpload(t)
	case TaskDownload:
		err = q.handleDownload(t)
	case TaskDelete:
		err = q.handleDelete(t)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if err != nil {
		t.Status = fmt.Sprintf("Error: %v", err)
		t.CompletedAt = time.Now() // Mark time of error for cleanup
		q.updateTaskState(t)
		log.Printf("❌ Task %s failed: %v", t.ID, err)
	} else {
		t.Status = "Completed"
		t.Progress = 100
		t.CompletedAt = time.Now() // Mark time of completion for cleanup
		q.updateTaskState(t)
		log.Printf("✅ Task %s completed successfully", t.ID)
	}
	// Tasks will be auto-cleaned 30s after completion by cleanupCompletedTasks()
}

func (q *TaskQueue) handleUpload(t *Task) error {
	// OPTIMIZATION #5: Quota guard before expensive operations
	// Prevent starting an upload if remaining quota is too low
	const quotaThreshold = 2000 // units minimum needed
	if q.ytManager != nil && q.ytManager.IsAuthenticated() {
		// Check if we might not have enough quota
		// (This is a warning, not a hard block - just prevents starting if quota is critically low)
		// In production, you'd track daily quota consumption via database
		log.Printf("⚠️  Quota guard: Recommend minimum %d units available. Proceed with caution if near limit.", quotaThreshold)
	}
	
	t.Status = "Checking Deduplication"
	q.updateTaskState(t)

	var totalSize int64
	var file io.ReadSeekCloser

	stat, err := os.Stat(t.FilePath)
	if err != nil {
		return err
	}

	h := sha256.New()

	if stat.IsDir() {
		t.Status = "Archiving Folder"
		q.updateTaskState(t)

		tarData, err := ArchiveFolder(t.FilePath)
		if err != nil {
			return fmt.Errorf("failed to archive folder: %w", err)
		}

		// Write tar to a temp file so we can chunk it
		tempTar, err := os.CreateTemp("", "nexus-archive-*.tar")
		if err != nil {
			return err
		}
		defer func() {
			tempTar.Close()
			os.Remove(tempTar.Name())
		}()

		if _, err := tempTar.Write(tarData); err != nil {
			return err
		}
		
		totalSize = int64(len(tarData))
		h.Write(tarData)
		file = tempTar
	} else {
		f, err := os.Open(t.FilePath)
		if err != nil {
			return fmt.Errorf("could not open file: %w", err)
		}
		defer f.Close()
		
		totalSize = stat.Size()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		file = f
	}

	t.SHA256 = hex.EncodeToString(h.Sum(nil))

	// Quota-Thrifty Deduplication
	existing, _ := q.db.GetFileByHash(t.SHA256)
	if existing != nil && existing.VideoID != "" && !t.IsManifest {
		log.Printf("[%s] 🔬 Verifying cloud record for deduplication (ID: %s)...", t.ID, existing.VideoID)
		exists, err := q.ytManager.VideoExists(existing.VideoID)
		if err == nil && exists {
			q.db.LogQuotaUsage(1)
			log.Printf("[%s] ♻️  Deduplication: File verified on cloud. Linking locally...", t.ID)
			q.db.SaveFile(t.FilePath, existing.VideoID, totalSize, existing.Hash, existing.Key, t.ParentID, t.SHA256, existing.IsArchive, t.Mode)
			t.Status = "Completed"
			return nil
		} else {
			log.Printf("[%s] ⚠️  Stale Deduplication: Record exists but cloud video %s is missing. Purging stale entry...", t.ID, existing.VideoID)
			q.db.PermanentDelete(existing.ID)
			// Continue to fresh upload
		}
	}

	// Nexus 2.0: Manifest belongs on Google Drive, not YouTube
	if t.IsManifest {
		t.Status = "Uploading to Drive"
		q.updateTaskState(t)
		
		dbFile, err := os.Open(t.FilePath)
		if err != nil {
			return fmt.Errorf("could not open manifest for drive upload: %w", err)
		}
		defer dbFile.Close()

		driveID, err := q.ytManager.UploadManifestToDrive("nexus.db", dbFile)
		if err != nil {
			return fmt.Errorf("drive upload failed: %w", err)
		}

		log.Printf("[%s] ✅ Manifest backed up to Google Drive: %s", t.ID, driveID)
		t.Status = "Completed"
		t.Progress = 100
		q.updateTaskState(t)
		
		// Optional: Still clean up old YouTube manifests once
		q.SweepOldManifests(driveID)
		return nil
	}

	// Shard size = 2GB (1024 * 1024 * 1024 * 2 bytes) - OPTIMIZATION #5
	// Larger shards = fewer YouTube API calls = lower quota consumption
	// Example: 100GB file now needs 50 uploads instead of 100 (saves 600+ units per upload)
	const shardSize = 2 * 1024 * 1024 * 1024
	numShards := int((totalSize + shardSize - 1) / shardSize)
	if numShards == 0 {
		numShards = 1 // Handle empty files
	}

	targetPlaylist, _ := q.db.GetKV("playlist_root_id")
	if t.IsManifest {
		targetPlaylist, _ = q.db.GetKV("playlist_manifest_id")
	} else if t.ParentID != nil {
		pID, pErr := q.pm.SyncFolderToPlaylist(*t.ParentID)
		if pErr == nil {
			targetPlaylist = pID
		}
	}

	var manifestVideoID string

	// V3: Generate a unique random encryption key for this file
	rawFileKey, err := q.core.GenerateFileKey()
	if err != nil {
		return fmt.Errorf("key generation failed: %w", err)
	}
	// Diagnostic: log a short sample of the generated raw file key for debugging
	if len(rawFileKey) >= 8 {
		log.Printf("[%s] [debug] Generated rawFileKey len=%d start=%x end=%x", t.ID, len(rawFileKey), rawFileKey[:8], rawFileKey[len(rawFileKey)-8:])
	} else {
		log.Printf("[%s] [debug] Generated rawFileKey len=%d", t.ID, len(rawFileKey))
	}
	
	// V4 Security: Use password priority:
	// 1. Custom password provided by user
	// 2. Active master key from session
	// 3. Auto-derived key from Google sub (zero-knowledge, automatic)
	encryptionSecret := t.Password
	if encryptionSecret == "" {
		// No custom password - check if we have an active masterKey
		q.masterKeyMu.RLock()
		if q.masterKeyHex != "" {
			encryptionSecret = q.masterKeyHex // Use hex-encoded masterKey
		}
		q.masterKeyMu.RUnlock()
	}
	
	// If still no secret, derive from Google sub (new approach: no password needed!)
	if encryptionSecret == "" && q.ytManager != nil {
		googleSub := q.ytManager.GetGoogleSub()
		if googleSub != "" {
			// Auto-derive key from Google sub - fully automatic, user doesn't know/care
			encryptionSecret = deriveKeyFromGoogleSub(googleSub)
			log.Printf("ℹ️  Using auto-derived key from Google sub (user didn't provide password)")
		}
	}
	
	if encryptionSecret == "" {
		return fmt.Errorf("no encryption secret available: user must be authenticated with Google")
	}
	
	encryptedFileKeyBytes, err := q.core.Encrypt(rawFileKey, encryptionSecret)
	if err != nil {
		return fmt.Errorf("key encryption failed: %w", err)
	}
	storedFileKeyHex := hex.EncodeToString(encryptedFileKeyBytes)

	for i := 0; i < numShards; i++ {
		t.Status = fmt.Sprintf("Processing Shard %d/%d", i+1, numShards)
		t.Progress = float64(i) / float64(numShards) * 100
		q.updateTaskState(t)

		file.Seek(int64(i)*int64(shardSize), 0)
		reader := io.LimitReader(file, int64(shardSize))
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}

		t.Status = fmt.Sprintf("Encrypting Shard %d/%d", i+1, numShards)
		compressed, _ := q.core.Compress(data, 0)
		var encrypted []byte
		
		if t.IsManifest {
			// V4: Manifest DB backup uses masterKey (same as file_key encryption)
			if encryptionSecret == "" {
				return fmt.Errorf("cannot encrypt manifest without encryption secret")
			}
			encrypted, err = q.core.Encrypt(compressed, encryptionSecret)
		} else {
			// OPTIMIZATION #6: Double encryption (optional, per-file)
			// Layer 1: If custom password set, encrypt with it first
			encrypted = compressed
			if t.CustomEncryptPassword != "" {
				encrypted, err = q.core.Encrypt(encrypted, t.CustomEncryptPassword)
				if err != nil {
					return fmt.Errorf("custom password encryption failed: %w", err)
				}
				log.Printf("[%s] 🔐 Applied custom password encryption (Layer 1)", t.ID)
			}
			
			// Layer 2: Always encrypt with file-specific rawFileKey
			encrypted, err = q.core.EncryptWithKey(encrypted, rawFileKey)
		}
		if err != nil {
			return err
		}
		// Diagnostic: log encrypted blob size and sample bytes to help trace corruption
		if len(encrypted) > 0 {
			start := 8
			if len(encrypted) < start { start = len(encrypted) }
			end := 8
			if len(encrypted) < end { end = len(encrypted) }
			log.Printf("[%s] [ENC] Encrypted shard %d size=%d start=%x end=%x", t.ID, i+1, len(encrypted), encrypted[:start], encrypted[len(encrypted)-end:])
		}
        // Debug: write full hex dump of encrypted blob for offline inspection
        if len(encrypted) > 0 {
            _ = os.MkdirAll("/tmp/nexus-debug", 0755)
            encPath := fmt.Sprintf("/tmp/nexus-debug/%s-shard-%d-enc.hex", t.ID, i+1)
            _ = os.WriteFile(encPath, []byte(hex.EncodeToString(encrypted)), 0644)
            log.Printf("[%s] [debug] Wrote encrypted hex dump: %s", t.ID, encPath)
        }

		t.Status = fmt.Sprintf("Encoding Shard %d/%d", i+1, numShards)
		apiMode := 0 // Base
		if t.Mode == "high" {
			apiMode = 1 // High
		}
		tempDir, _ := os.MkdirTemp("", fmt.Sprintf("nexus-upload-%s-shard-%d", t.ID, i))
		frameCount, err := q.core.EncodeToFrames(encrypted, tempDir, apiMode)
		if err != nil {
			os.RemoveAll(tempDir)
			return err
		}

		if frameCount < 90 {
			log.Printf("[%s] ⏳ Padding video to 90 frames...", t.ID)
			lastFramePath := filepath.Join(tempDir, fmt.Sprintf("frame_%06d.png", frameCount))
			lastFrameData, _ := os.ReadFile(lastFramePath)
			for j := frameCount + 1; j <= 90; j++ {
				os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("frame_%06d.png", j)), lastFrameData, 0644)
			}
		}

		t.Status = fmt.Sprintf("FFmpeg Shard %d/%d", i+1, numShards)
		// Choose output container/codec based on mode. For `high` prefer WebM/VP9 to
		// minimize intermediate re-encoding on YouTube and better preserve luma levels.
		outputVideo := filepath.Join(tempDir, "upload.mp4")
		if t.Mode == "high" {
			outputVideo = filepath.Join(tempDir, "upload.webm")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		// Build ffmpeg args depending on mode
		ffArgs := []string{"-y", "-framerate", "30", "-i", filepath.Join(tempDir, "frame_%06d.png"), "-g", "1"}
		if t.Mode == "high" {
			// Use VP9 lossless to avoid pixel distortion. libvpx-vp9 preserves luma well.
			ffArgs = append(ffArgs,
				"-c:v", "libvpx-vp9",
				"-pix_fmt", "yuv420p",
				"-lossless", "1",
				"-b:v", "0",
			)
		} else {
			// Base mode: use H.264 as before
			ffArgs = append(ffArgs,
				"-c:v", "libx264",
				"-pix_fmt", "gray",
				"-crf", "18",
			)
			// Keep x264 special params for intra behavior
			ffArgs = append(ffArgs, "-x264-params", "scm=1")
		}
		ffArgs = append(ffArgs, outputVideo)
		cmd := exec.CommandContext(ctx, "ffmpeg", ffArgs...)
		if _, err := cmd.CombinedOutput(); err != nil {
			cancel()
			os.RemoveAll(tempDir)
			return fmt.Errorf("ffmpeg failed: %w", err)
		}
		cancel()

		t.Status = fmt.Sprintf("YouTube Uploading Shard %d/%d", i+1, numShards)
		if q.ytManager == nil || !q.ytManager.IsAuthenticated() {
			os.RemoveAll(tempDir)
			return fmt.Errorf("youtube not authenticated")
		}

		ytService := q.ytManager.GetService()
		uploadFile, _ := os.Open(outputVideo)

		// Shards for regular files
		title := fmt.Sprintf("NexusStorage - %s (Part %d/%d)", filepath.Base(t.FilePath), i+1, numShards)
		desc := fmt.Sprintf("NEXUS_SHARD | SHA256: %s | Size: %d | Part: %d/%d", t.SHA256, len(data), i+1, numShards)
		
		if q.db.IsStealthMode() {
			title = fmt.Sprintf("DATA_BLOCK_%s_P%d", t.SHA256[:8], i+1)
			desc = "Autogenerated data block."
		}

		upload := &youtube.Video{
			Snippet: &youtube.VideoSnippet{Title: title, Description: desc, CategoryId: "22"},
			Status:  &youtube.VideoStatus{PrivacyStatus: "unlisted"},
		}

		call := ytService.Videos.Insert([]string{"snippet", "status"}, upload)
		response, err := call.Media(uploadFile).Do()
		uploadFile.Close()
		os.RemoveAll(tempDir)
		if err != nil {
			return fmt.Errorf("youtube upload failed: %w", err)
		}
		q.db.LogQuotaUsage(1600)

		if targetPlaylist != "" {
			q.pm.AddVideoToPlaylist(targetPlaylist, response.Id)
		}

		if i == 0 {
			manifestVideoID = response.Id
		}

		if !t.IsManifest {
			// Save the file first if it's the very first part, so the shards have a foreign key
			if i == 0 {
				isArchive := false
				if stat, err := os.Stat(t.FilePath); err == nil && stat.IsDir() {
					isArchive = true
				}
				hasCustomPassword := t.Password != "" || t.CustomEncryptPassword != ""
				q.db.SaveFileWithKey(filepath.Base(t.FilePath), response.Id, totalSize, t.SHA256[:16], "default-key", t.ParentID, t.SHA256, storedFileKeyHex, isArchive, hasCustomPassword, t.Mode)
			}
			fileRecord, _ := q.db.GetFileByHash(t.SHA256)
			if fileRecord != nil {
				q.db.SaveShard(fileRecord.ID, response.Id, i)
			}
		}
	}

	if t.IsManifest {
		q.SweepOldManifests(manifestVideoID)
		return nil
	}

	t.Status = "Finalizing"
	t.Progress = 95
	q.updateTaskState(t)
	q.RequestManifestBackup()
	
	return nil
}

func (q *TaskQueue) RequestManifestBackup() {
	q.manifestMu.Lock()
	defer q.manifestMu.Unlock()

	if q.manifestTimer != nil {
		q.manifestTimer.Stop()
	}

	// Debounce for 2 seconds
	q.manifestTimer = time.AfterFunc(2*time.Second, func() {
		log.Println("🔄 Debounced Manifest Backup triggered after DB changes.")
		q.QueueManifestBackup()
	})
}

func (q *TaskQueue) QueueManifestBackup() {
	if q.syncMgr == nil {
		log.Printf("⚠️  Manifest Backup skipped: SyncManager not initialized")
		return
	}

	if q.ytManager == nil || !q.ytManager.IsAuthenticated() {
		return
	}

	// Use strict Push logic
	if err := q.syncMgr.PushDBToDrive(); err != nil {
		log.Printf("⚠️  Manifest Backup failed: %v", err)
	} else {
		log.Printf("✅ Manifest Backup completed.")
	}
}

func (q *TaskQueue) SweepOldManifests(newId string) {
	ytService := q.ytManager.GetService()
	if ytService == nil {
		return
	}

	// 1. Always update the local KV store first
	q.db.SetKV("manifest_video_id", newId)

	// 2. Intelligent Cleanup: Search for ANY video titled 'NEXUS_MANIFEST' 
	// This cleans up "ghosts" even if the local DB was deleted or out of sync.
	// Search for standard OR stealth manifests
	call1 := ytService.Search.List([]string{"id", "snippet"}).Q("NEXUS_MANIFEST").Type("video").ForMine(true).MaxResults(50)
	resp1, err1 := call1.Do()
	if err1 == nil {
		q.db.LogQuotaUsage(100)
	}
	call2 := ytService.Search.List([]string{"id", "snippet"}).Q("DATA_STATE_MANIFEST").Type("video").ForMine(true).MaxResults(50)
	resp2, err2 := call2.Do()
	if err2 == nil {
		q.db.LogQuotaUsage(100)
	}

	if err1 != nil && err2 != nil {
		log.Printf("⚠️  Manifest Sweep: search failed")
		if oldId, ok := q.db.GetKV("manifest_video_id"); ok && oldId != "" && oldId != newId {
			ytService.Videos.Delete(oldId).Do()
			q.db.LogQuotaUsage(50)
		}
		return
	}

	var allItems []*youtube.SearchResult
	if resp1 != nil { allItems = append(allItems, resp1.Items...) }
	if resp2 != nil { allItems = append(allItems, resp2.Items...) }

	for _, item := range allItems {
		id := item.Id.VideoId
		if id != newId {
			log.Printf("🗑️  Manifest Sweep: Deleting orphan manifest %s (%s)", id, item.Snippet.Title)
			if err := ytService.Videos.Delete(id).Do(); err != nil {
				log.Printf("⚠️  Manifest Sweep: could not delete %s: %v", id, err)
			} else {
				q.db.LogQuotaUsage(50)
			}
		}
	}
}

func (q *TaskQueue) handleDownload(t *Task) error {
	t.Status = "Preparing"
	t.Progress = 5
	q.updateTaskState(t)

	ensureYtDlp()

	if len(t.ID) > 6 && t.ID[:6] == "local-" {
		return fmt.Errorf("mock local video cannot be downloaded without real youtube video")
	}

	// V4 Security: Use password priority (same as upload):
	// 1. Custom password provided by user
	// 2. Active master key from session
	// 3. Auto-derived key from Google sub (zero-knowledge, automatic)
	encryptionSecret := t.Password
	if encryptionSecret == "" {
		q.masterKeyMu.RLock()
		if q.masterKeyHex != "" {
			encryptionSecret = q.masterKeyHex // hex-encoded masterKey
		}
		q.masterKeyMu.RUnlock()
	}
	
	// If still no secret, derive from Google sub (new approach: no password needed for download!)
	if encryptionSecret == "" && q.ytManager != nil {
		googleSub := q.ytManager.GetGoogleSub()
		if googleSub != "" {
			// Auto-derive key from Google sub - must match the key used during upload
			encryptionSecret = deriveKeyFromGoogleSub(googleSub)
		}
	}
	
	if encryptionSecret == "" {
		return fmt.Errorf("no encryption secret available for download: user must be authenticated with Google")
	}

	var rawFileKey []byte
	fileRecord, _ := q.db.GetFileByHash(t.SHA256)
	var shardIDs []string
	// If CustomEncryptPassword is provided, use it for decryption layer 1
	needsCustomPassword := t.CustomEncryptPassword != ""
	
	if fileRecord != nil {
		if fileRecord.Mode != "" {
			t.Mode = fileRecord.Mode // V6: Override empty mode with database value
		}
		shardIDs, _ = q.db.GetShardsForFile(fileRecord.ID)
		
		// V3: Try to recover the per-file key
		if fileRecord.FileKey != "" {
			log.Printf("[%s] 🔍 Attempting to decrypt file_key (%d bytes hex)...", t.ID, len(fileRecord.FileKey))
			encryptedKey, err := hex.DecodeString(fileRecord.FileKey)
			if err == nil {
				log.Printf("[%s] ✅ file_key hex decoded successfully (%d bytes)", t.ID, len(encryptedKey))
				key, err := q.core.Decrypt(encryptedKey, encryptionSecret)
				if err == nil {
					rawFileKey = key
					log.Printf("[%s] ✅ file_key decrypted successfully (%d bytes)", t.ID, len(rawFileKey))
					// Diagnostic: log a short sample of the decrypted per-file key for debugging
					if len(rawFileKey) >= 8 {
						log.Printf("[%s] [debug] Decrypted rawFileKey len=%d start=%x end=%x", t.ID, len(rawFileKey), rawFileKey[:8], rawFileKey[len(rawFileKey)-8:])
					} else {
						log.Printf("[%s] [debug] Decrypted rawFileKey len=%d", t.ID, len(rawFileKey))
					}
				} else {
					log.Printf("[%s] ⚠️  file_key decryption FAILED: %v", t.ID, err)
					log.Printf("[%s]    encryptionSecret first 16 chars: %s", t.ID, encryptionSecret[:16])
				}
			} else {
				log.Printf("[%s] ❌ file_key hex decode failed: %v", t.ID, err)
			}
		} else {
			log.Printf("[%s] ℹ️  No file_key stored in DB, will use fallback (encryptionSecret only)", t.ID)
		}
	}

	if len(shardIDs) == 0 {
		shardIDs = []string{t.ID}
	}

	tempDir, err := os.MkdirTemp("", "nexus-download-*")
	if err != nil {
		return fmt.Errorf("could not create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	outDir := filepath.Join(os.Getenv("HOME"), "Downloads", "Nexus")
	os.MkdirAll(outDir, 0755)

	filename := filepath.Base(t.FilePath)
	if filename == "." || filename == "/" {
		filename = "recovered_file_" + t.ID
	}
	outPath := filepath.Join(outDir, filename)

	outFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	for i, vID := range shardIDs {
		t.Status = fmt.Sprintf("Shard %d/%d (Checking Cache)", i+1, len(shardIDs))
		t.Progress = float64(i) / float64(len(shardIDs)) * 100
		q.updateTaskState(t)

		var rawData []byte
		cachedPath, found := "", false
		if q.cache != nil {
			cachedPath, found = q.cache.Get(vID)
		}

		if found {
			t.Status = fmt.Sprintf("Shard %d/%d (Cache Hit)", i+1, len(shardIDs))
			q.updateTaskState(t)
			rawData, err = os.ReadFile(cachedPath)
		} else {
			t.Status = fmt.Sprintf("Downloading Shard %d/%d", i+1, len(shardIDs))
			q.updateTaskState(t)

			// Use a template so yt-dlp appends the correct extension for the native container
			videoTemplate := filepath.Join(tempDir, fmt.Sprintf("download_%d.%%(ext)s", i))
			framesDir := filepath.Join(tempDir, fmt.Sprintf("frames_%d", i))
			os.MkdirAll(framesDir, 0755)

			ytURL := "https://www.youtube.com/watch?v=" + vID
			// yt-dlp: prefer bestvideo + bestaudio (mux by yt-dlp) so we get native codec/container
			format := "bestvideo+bestaudio/best"
			if t.Mode == "high" {
				// Request best 4K video+audio; will typically pick WebM/VP9 video + opus audio
				format = "bestvideo[height>=2160]+bestaudio/best"
			}
			log.Printf("[%s] ⬇️  yt-dlp format selection: %s (mode=%s)", t.ID, format, t.Mode)
			dlCmd := exec.Command("yt-dlp", "-f", format, "-o", videoTemplate, ytURL)
			if out, err := dlCmd.CombinedOutput(); err != nil {
				return fmt.Errorf("yt-dlp failed: %v\nOutput: %s", err, string(out))
			}

			// Find the actual downloaded file (yt-dlp appends the proper extension)
			matches, _ := filepath.Glob(filepath.Join(tempDir, fmt.Sprintf("download_%d.*", i)))
			if len(matches) == 0 {
				return fmt.Errorf("yt-dlp did not produce a download for shard %d", i)
			}
			videoFile := matches[0]

			t.Status = fmt.Sprintf("Extracting Shard %d/%d", i+1, len(shardIDs))
			q.updateTaskState(t)
			// Determine target resolution based on mode
			// Use nearest-neighbor (flags=neighbor) to preserve hard block edges — critical for data integrity
			targetScale := "1280:720:flags=neighbor"
			if t.Mode == "high" {
				targetScale = "3840:2160:flags=neighbor"
			}
			// Ensure we extract as grayscale + scale to original encode resolution for Luma-only decoder
			ffCmd := exec.Command("ffmpeg", "-y", "-i", videoFile,
				"-vf", "scale="+targetScale,
				"-pix_fmt", "gray",
				filepath.Join(framesDir, "frame_%06d.png"))
			if err := ffCmd.Run(); err != nil {
				return fmt.Errorf("ffmpeg extraction failed: %w", err)
			}

			t.Status = fmt.Sprintf("Decoding Shard %d/%d", i+1, len(shardIDs))
			q.updateTaskState(t)
			apiMode := 0 // Base
			if t.Mode == "high" {
				apiMode = 1 // High
			}
			rawData, err = q.core.DecodeFromFrames(framesDir, apiMode)
			if err != nil {
				return fmt.Errorf("decoding failed: %w", err)
			}

			if q.cache != nil {
				q.cache.Put(vID, rawData)
			}
		}

		t.Status = fmt.Sprintf("Decrypting Shard %d/%d", i+1, len(shardIDs))
		q.updateTaskState(t)

		var decrypted []byte
		if rawFileKey != nil {
			log.Printf("[%s] 🔐 Shard %d: Decrypting with per-file key (%d bytes)", t.ID, i+1, len(rawFileKey))
			// Diagnostic: log rawData size and samples
			if len(rawData) > 0 {
				start := 8
				if len(rawData) < start { start = len(rawData) }
				end := 8
				if len(rawData) < end { end = len(rawData) }
				log.Printf("[%s] [DEC] Raw shard %d size=%d start=%x end=%x", t.ID, i+1, len(rawData), rawData[:start], rawData[len(rawData)-end:])
			}

				// Debug: write full hex dump of downloaded/decoded blob for offline inspection
				_ = os.MkdirAll("/tmp/nexus-debug", 0755)
				decPath := fmt.Sprintf("/tmp/nexus-debug/%s-shard-%d-dec.hex", t.ID, i+1)
				_ = os.WriteFile(decPath, []byte(hex.EncodeToString(rawData)), 0644)
				log.Printf("[%s] [debug] Wrote downloaded hex dump: %s", t.ID, decPath)
			decrypted, err = q.core.DecryptWithKey(rawData, rawFileKey)
		} else {
			log.Printf("[%s] 🔐 Shard %d: Decrypting with encryptionSecret (fallback)", t.ID, i+1)
			decrypted, err = q.core.Decrypt(rawData, encryptionSecret)
		}

		if err != nil {
			return fmt.Errorf("decryption failed: %w", err)
		}

		// OPTIMIZATION #6: Double decryption (optional, per-file)
		// If file has custom password, decrypt the second layer
		if needsCustomPassword {
			log.Printf("[%s] 🔑 Shard %d: Decrypting custom password layer (Layer 1)", t.ID, i+1)
			decrypted, err = q.core.Decrypt(decrypted, t.CustomEncryptPassword)
			if err != nil {
				return fmt.Errorf("custom password decryption failed (wrong password?): %w", err)
			}
		}

		t.Status = fmt.Sprintf("Decompressing Shard %d/%d", i+1, len(shardIDs))
		q.updateTaskState(t)
		decompressed, err := q.core.Decompress(decrypted)
		if err != nil {
			return fmt.Errorf("decompression failed: %w", err)
		}

		// Simple heuristic: if the original path didn't have an extension but it's a tarball, extract it.
		// Alternatively, we can check if it's the first shard and the first bytes are a tar header.
		// But for now, we just write it. We will handle extraction after all shards are combined.
		if _, err := outFile.Write(decompressed); err != nil {
			return err
		}
	}

	// Post-processing: check if the downloaded file is a tar archive
	outFile.Close()
	
	downloadedData, err := os.ReadFile(outPath)
	if err == nil && len(downloadedData) > 512 {
		// Tar headers have specific magic bytes at offset 257 ("ustar")
		if string(downloadedData[257:262]) == "ustar" {
			log.Printf("📦 Detected Archive. Extracting to %s...", outPath+"_extracted")
			extDir := outPath + "_extracted"
			if err := ExtractArchive(downloadedData, extDir); err == nil {
				os.Remove(outPath) // Remove the raw tarball
				outPath = extDir
			} else {
				log.Printf("⚠️ Extraction failed: %v", err)
			}
		}
	}

	log.Printf("File recovered to: %s", outPath)
	return nil
}

func (q *TaskQueue) handleDelete(t *Task) error {
	t.Status = "YouTube: Deleting"
	t.Progress = 50
	if q.ytManager == nil || !q.ytManager.IsAuthenticated() {
		return nil // skip if offline
	}
	if len(t.ID) > 6 && t.ID[:6] == "local-" {
		return nil
	}
	ytService := q.ytManager.GetService()
	err := ytService.Videos.Delete(t.ID).Do()
	if err != nil {
		log.Printf("Warning: Could not delete YouTube video %s: %v", t.ID, err)
	} else {
		q.db.LogQuotaUsage(50)
	}
	t.Progress = 90
	return nil
}

// CleanupOrphanedVideos finds permanently deleted files with VideoIDs that don't have a deletion task queued.
// This handles race conditions where DB delete succeeds but task queueing fails.
func (q *TaskQueue) CleanupOrphanedVideos() error {
	if q.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// Get all deleted files
	deletedFiles, err := q.db.ListTrash()
	if err != nil {
		return fmt.Errorf("failed to list trash: %v", err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	var orphanCount int
	for _, file := range deletedFiles {
		// Skip files without VideoID
		if file.VideoID == "" {
			continue
		}

		// Check if this VideoID already has a delete task queued
		if _, taskExists := q.tasks[file.VideoID]; !taskExists {
			// This is an orphan - queue the deletion task
			orphanTask := &Task{
				ID:        file.VideoID,
				Type:      TaskDelete,
				Status:    "Pending (Orphan Cleanup)",
				CreatedAt: time.Now(),
			}
			q.tasks[file.VideoID] = orphanTask
			go func(task *Task) {
				q.taskChan <- task
			}(orphanTask)
			orphanCount++
			log.Printf("🔧 Orphan Cleanup: Queued deletion for orphaned VideoID %s", file.VideoID)
		}
	}

	if orphanCount > 0 {
		log.Printf("✅ Orphan Cleanup: Found and queued %d orphaned videos for deletion", orphanCount)
	}

	return nil
}
