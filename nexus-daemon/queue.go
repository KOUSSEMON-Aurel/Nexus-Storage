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
	"sync"
	"time"

	"google.golang.org/api/youtube/v3"
)

type TaskType int

const (
	TaskUpload TaskType = iota
	TaskDownload
	TaskDelete
)

type Task struct {
	ID         string    `json:"id"`
	Type       TaskType  `json:"type"`
	FilePath   string    `json:"filePath"`
	Mode       string    `json:"mode"`
	IsManifest bool      `json:"isManifest"`
	Status     string    `json:"status"`
	Progress   float64   `json:"progress"`
	CreatedAt  time.Time `json:"createdAt"`
	ParentID   *int64    `json:"parentId"`
	SHA256     string    `json:"sha256,omitempty"`
}

type TaskQueue struct {
	tasks         map[string]*Task
	mu            sync.Mutex
	core          *NexusCore
	db            *Database
	ytManager     *YouTubeManager
	manifestMu    sync.Mutex
	manifestTimer *time.Timer
	pm            *PlaylistManager
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

func (q *TaskQueue) Init(core *NexusCore, db *Database, ytManager *YouTubeManager, pm *PlaylistManager) {
	q.tasks = make(map[string]*Task)
	q.core = core
	q.db = db
	q.ytManager = ytManager
	q.pm = pm
	ensureYtDlp() // synchronous - must complete before any tasks

	// Load pending tasks from DB
	rows, err := db.GetPendingTasks()
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			t := &Task{}
			var tType int
			err := rows.Scan(&t.ID, &tType, &t.FilePath, &t.Mode, &t.IsManifest, &t.Status, &t.Progress, &t.CreatedAt, &t.ParentID, &t.SHA256)
			if err == nil {
				t.Type = TaskType(tType)
				q.tasks[t.ID] = t
				log.Printf("⏳ Resuming pending task %s", t.ID)
				go q.processTask(t)
			}
		}
	}
}

func (q *TaskQueue) AddTask(t *Task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks[t.ID] = t
	q.db.SaveTask(t.ID, int(t.Type), t.FilePath, t.Mode, t.IsManifest, t.Status, t.Progress, t.CreatedAt, t.ParentID, t.SHA256)
	go q.processTask(t)
}

func (q *TaskQueue) updateTaskState(t *Task) {
	q.db.SaveTask(t.ID, int(t.Type), t.FilePath, t.Mode, t.IsManifest, t.Status, t.Progress, t.CreatedAt, t.ParentID, t.SHA256)
}

func (q *TaskQueue) processTask(t *Task) {
	t.Status = "Processing"
	q.updateTaskState(t)
	log.Printf("Starting task %s", t.ID)

	var err error
	switch t.Type {
	case TaskUpload:
		err = q.handleUpload(t)
	case TaskDownload:
		err = q.handleDownload(t)
	case TaskDelete:
		err = q.handleDelete(t)
	}

	if err != nil {
		t.Status = fmt.Sprintf("Error: %v", err)
		q.updateTaskState(t)
		log.Printf("Task %s failed: %v", t.ID, err)
	} else {
		t.Status = "Completed"
		t.Progress = 100
		q.db.DeleteTask(t.ID) // Remove from queue on success
		log.Printf("Task %s completed successfully", t.ID)
	}
}

func (q *TaskQueue) handleUpload(t *Task) error {
	t.Status = "Checking Deduplication"
	q.updateTaskState(t)

	file, err := os.Open(t.FilePath)
	if err != nil {
		return fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return err
	}
	totalSize := stat.Size()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	t.SHA256 = hex.EncodeToString(h.Sum(nil))

	// Quota-Thrifty Deduplication
	existing, _ := q.db.GetFileByHash(t.SHA256)
	if existing != nil {
		log.Printf("[%s] ♻️  Deduplication: File already exists. Linking locally...", t.ID)
		t.Status = "Linked (Dedupe)"
		t.Progress = 100
		q.updateTaskState(t)
		return q.db.SaveFile(filepath.Base(t.FilePath), existing.VideoID, totalSize, "dedupe", "dedupe", t.ParentID, t.SHA256)
	}

	// Shard size = 1GB (1024 * 1024 * 1024 bytes)
	const shardSize = 1024 * 1024 * 1024
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
		encrypted, err := q.core.Encrypt(compressed, "default-secret")
		if err != nil {
			return err
		}

		t.Status = fmt.Sprintf("Encoding Shard %d/%d", i+1, numShards)
		apiMode := 0
		if t.Mode == "density" {
			apiMode = 1
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
		outputVideo := filepath.Join(tempDir, "upload.mp4")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-framerate", "30", "-i", filepath.Join(tempDir, "frame_%06d.png"),
			"-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", outputVideo)
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

		title := fmt.Sprintf("NexusStorage - %s (Part %d/%d)", filepath.Base(t.FilePath), i+1, numShards)
		desc := fmt.Sprintf("NEXUS_SHARD | SHA256: %s | Size: %d | Part: %d/%d", t.SHA256, len(data), i+1, numShards)
		
		if q.db.IsStealthMode() {
			title = fmt.Sprintf("DATA_BLOCK_%s_P%d", t.SHA256[:8], i+1)
			desc = "Autogenerated data block."
		}

		if t.IsManifest {
			title = "NEXUS_MANIFEST"
			desc = fmt.Sprintf("NEXUS_MANIFEST | Backup: %v", time.Now().Format(time.RFC3339))
			if q.db.IsStealthMode() {
				title = "DATA_STATE_MANIFEST"
				desc = "Autogenerated state data."
			}
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
				q.db.SaveFile(filepath.Base(t.FilePath), response.Id, totalSize, t.SHA256[:16], "default-key", t.ParentID, t.SHA256)
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

	// Debounce for 30 seconds
	q.manifestTimer = time.AfterFunc(30*time.Second, func() {
		log.Println("🔄 Debounced Manifest Backup triggered after DB changes.")
		q.QueueManifestBackup()
	})
}

func (q *TaskQueue) QueueManifestBackup() {
	if q.ytManager == nil || !q.ytManager.IsAuthenticated() {
		return
	}
	dbPath := filepath.Join(getConfigDir(), "nexus.db")
	t := &Task{
		ID:         fmt.Sprintf("manifest-%d", time.Now().UnixNano()),
		Type:       TaskUpload,
		FilePath:   dbPath,
		Mode:       "tank",
		IsManifest: true,
		Status:     "Pending Manifest",
		CreatedAt:  time.Now(),
	}
	q.AddTask(t)
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
	call2 := ytService.Search.List([]string{"id", "snippet"}).Q("DATA_STATE_MANIFEST").Type("video").ForMine(true).MaxResults(50)
	resp2, err2 := call2.Do()

	if err1 != nil && err2 != nil {
		log.Printf("⚠️  Manifest Sweep: search failed")
		if oldId, ok := q.db.GetKV("manifest_video_id"); ok && oldId != "" && oldId != newId {
			ytService.Videos.Delete(oldId).Do()
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

	fileRecord, _ := q.db.GetFileByHash(t.SHA256) 
	var shardIDs []string
	if fileRecord != nil {
		shardIDs, _ = q.db.GetShardsForFile(fileRecord.ID)
	}
	if len(shardIDs) == 0 {
		// Fallback: no shards found, just download the provided ID
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
		t.Status = fmt.Sprintf("Downloading Shard %d/%d", i+1, len(shardIDs))
		t.Progress = float64(i) / float64(len(shardIDs)) * 100
		q.updateTaskState(t)

		videoFile := filepath.Join(tempDir, fmt.Sprintf("download_%d.mp4", i))
		framesDir := filepath.Join(tempDir, fmt.Sprintf("frames_%d", i))
		os.MkdirAll(framesDir, 0755)

		ytURL := "https://www.youtube.com/watch?v=" + vID
		dlCmd := exec.Command("yt-dlp", "-f", "bestvideo[ext=mp4]", "-o", videoFile, ytURL)
		if err := dlCmd.Run(); err != nil {
			return fmt.Errorf("yt-dlp download failed: %w", err)
		}

		t.Status = fmt.Sprintf("Extracting Shard %d/%d", i+1, len(shardIDs))
		q.updateTaskState(t)
		ffCmd := exec.Command("ffmpeg", "-i", videoFile, filepath.Join(framesDir, "frame_%06d.png"))
		if err := ffCmd.Run(); err != nil {
			return fmt.Errorf("ffmpeg frame extraction failed: %w", err)
		}

		t.Status = fmt.Sprintf("Decoding Shard %d/%d", i+1, len(shardIDs))
		apiMode := 0
		if t.Mode == "density" {
			apiMode = 1
		}
		rawData, err := q.core.DecodeFromFrames(framesDir, apiMode)
		if err != nil {
			return fmt.Errorf("frame decoding failed: %w", err)
		}

		t.Status = fmt.Sprintf("Decrypting Shard %d/%d", i+1, len(shardIDs))
		decrypted, err := q.core.Decrypt(rawData, "default-secret")
		if err != nil {
			return fmt.Errorf("decryption failed: %w", err)
		}

		t.Status = fmt.Sprintf("Decompressing Shard %d/%d", i+1, len(shardIDs))
		decompressed, err := q.core.Decompress(decrypted)
		if err != nil {
			return fmt.Errorf("decompression failed: %w", err)
		}

		// Append to final file
		if _, err := outFile.Write(decompressed); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
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
	}
	t.Progress = 90
	return nil
}
