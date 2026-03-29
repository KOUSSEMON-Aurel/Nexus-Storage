package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
}

func (q *TaskQueue) AddTask(t *Task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks[t.ID] = t
	go q.processTask(t)
}

func (q *TaskQueue) processTask(t *Task) {
	t.Status = "Processing"
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
		log.Printf("Task %s failed: %v", t.ID, err)
	} else {
		t.Status = "Completed"
		t.Progress = 100
		log.Printf("Task %s completed successfully", t.ID)
	}
}

func (q *TaskQueue) handleUpload(t *Task) error {
	t.Status = "Checking Deduplication"
	data, err := os.ReadFile(t.FilePath)
	if err != nil {
		return fmt.Errorf("could not read file: %w", err)
	}

	sha := sha256.Sum256(data)
	t.SHA256 = hex.EncodeToString(sha[:])

	// Quota-Thrifty: Avoid upload if file already exists in cloud (0 units)
	existing, _ := q.db.GetFileByHash(t.SHA256)
	if existing != nil {
		log.Printf("[%s] ♻️  Deduplication: File already exists as %s. Linking locally...", t.ID, existing.VideoID)
		t.Status = "Linked (Dedupe)"
		t.Progress = 100
		return q.db.SaveFile(filepath.Base(t.FilePath), existing.VideoID, int64(len(data)), "dedupe", "dedupe", t.ParentID, t.SHA256)
	}

	t.Status = "Encrypting"
	t.Progress = 15
	compressed, _ := q.core.Compress(data, 0)
	encrypted, err := q.core.Encrypt(compressed, "default-secret")
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	t.Status = "Generating Frames"
	t.Progress = 30
	apiMode := 0
	if t.Mode == "density" {
		apiMode = 1
	}
	tempDir, _ := os.MkdirTemp("", "nexus-upload-*")
	defer os.RemoveAll(tempDir)

	frameCount, err := q.core.EncodeToFrames(encrypted, tempDir, apiMode)
	if err != nil {
		return fmt.Errorf("frame encoding failed: %w", err)
	}

	if frameCount < 90 {
		log.Printf("[%s] ⏳ Padding video to 90 frames...", t.ID)
		lastFramePath := filepath.Join(tempDir, fmt.Sprintf("frame_%06d.png", frameCount))
		lastFrameData, _ := os.ReadFile(lastFramePath)
		for i := frameCount + 1; i <= 90; i++ {
			os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("frame_%06d.png", i)), lastFrameData, 0644)
		}
	}

	t.Status = "FFmpeg: MP4"
	t.Progress = 50
	outputVideo := filepath.Join(tempDir, "upload.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-framerate", "30", "-i", filepath.Join(tempDir, "frame_%06d.png"),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", outputVideo)
	if _, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	t.Status = "YouTube: Uploading"
	t.Progress = 70
	if q.ytManager == nil || !q.ytManager.IsAuthenticated() {
		return fmt.Errorf("youtube not authenticated")
	}

	ytService := q.ytManager.GetService()
	uploadFile, _ := os.Open(outputVideo)
	defer uploadFile.Close()

	title := "NexusStorage - " + filepath.Base(t.FilePath)
	desc := fmt.Sprintf("NEXUS_SHARD | SHA256: %s | Size: %d", t.SHA256, len(data))
	if t.IsManifest {
		title = "NEXUS_MANIFEST"
		desc = fmt.Sprintf("NEXUS_MANIFEST | Backup: %v", time.Now().Format(time.RFC3339))
	}

	upload := &youtube.Video{
		Snippet: &youtube.VideoSnippet{Title: title, Description: desc, CategoryId: "22"},
		Status:  &youtube.VideoStatus{PrivacyStatus: "unlisted"},
	}

	call := ytService.Videos.Insert([]string{"snippet", "status"}, upload)
	response, err := call.Media(uploadFile).Do()
	if err != nil {
		return fmt.Errorf("youtube upload failed: %w", err)
	}
	q.db.LogQuotaUsage(1600)

	// Automatic Playlist Placement (V2 Cloud Structure)
	targetPlaylist, _ := q.db.GetKV("playlist_root_id")
	if t.IsManifest {
		targetPlaylist, _ = q.db.GetKV("playlist_manifest_id")
	} else if t.ParentID != nil {
		// If folder has a playlist, move it there (50 units)
		pID, pErr := q.pm.SyncFolderToPlaylist(*t.ParentID)
		if pErr == nil {
			targetPlaylist = pID
		}
	}

	if targetPlaylist != "" {
		q.pm.AddVideoToPlaylist(targetPlaylist, response.Id)
	}

	if t.IsManifest {
		q.SweepOldManifests(response.Id)
		return nil
	}

	t.Status = "Finalizing"
	t.Progress = 95
	err = q.db.SaveFile(filepath.Base(t.FilePath), response.Id, int64(len(data)), t.SHA256[:16], "default-key", t.ParentID, t.SHA256)
	if err == nil {
		q.RequestManifestBackup()
	}
	return err
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
	call := ytService.Search.List([]string{"id", "snippet"}).
		Q("NEXUS_MANIFEST").
		Type("video").
		ForMine(true).
		MaxResults(50)

	response, err := call.Do()
	if err != nil {
		log.Printf("⚠️  Manifest Sweep: search failed: %v", err)
		// Fallback: try to delete just the one we knew about from KV
		if oldId, ok := q.db.GetKV("manifest_video_id"); ok && oldId != "" && oldId != newId {
			ytService.Videos.Delete(oldId).Do()
		}
		return
	}

	for _, item := range response.Items {
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

	tempDir, err := os.MkdirTemp("", "nexus-download-*")
	if err != nil {
		return fmt.Errorf("could not create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	videoFile := filepath.Join(tempDir, "download.mp4")
	framesDir := filepath.Join(tempDir, "frames")
	if err := os.MkdirAll(framesDir, 0755); err != nil {
		return fmt.Errorf("could not create frames dir: %w", err)
	}

	t.Status = "YouTube: Downloading"
	t.Progress = 10

	ensureYtDlp() // Make sure yt-dlp is available before trying

	// handle local bypass
	if len(t.ID) > 6 && t.ID[:6] == "local-" {
		return fmt.Errorf("mock local video cannot be downloaded without real youtube video")
	}

	ytURL := "https://www.youtube.com/watch?v=" + t.ID
	dlCmd := exec.Command("yt-dlp", "-f", "bestvideo[ext=mp4]", "-o", videoFile, ytURL)
	if err := dlCmd.Run(); err != nil {
		return fmt.Errorf("yt-dlp download failed: %w", err)
	}

	t.Status = "FFmpeg: Extracting"
	t.Progress = 40
	ffCmd := exec.Command("ffmpeg", "-i", videoFile, filepath.Join(framesDir, "frame_%06d.png"))
	if err := ffCmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg frame extraction failed: %w", err)
	}

	t.Status = "Decoding"
	t.Progress = 65
	apiMode := 0
	if t.Mode == "density" {
		apiMode = 1
	}
	rawData, err := q.core.DecodeFromFrames(framesDir, apiMode)
	if err != nil {
		return fmt.Errorf("frame decoding failed: %w", err)
	}

	t.Status = "Decrypting"
	t.Progress = 80
	decrypted, err := q.core.Decrypt(rawData, "default-secret")
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}

	t.Status = "Decompressing"
	t.Progress = 90
	decompressed, err := q.core.Decompress(decrypted)
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}

	t.Status = "Saving File"
	t.Progress = 95
	outDir := filepath.Join(os.Getenv("HOME"), "Downloads", "Nexus")
	os.MkdirAll(outDir, 0755)
	
	// Ensure we preserve the original base name
	filename := filepath.Base(t.FilePath)
	if filename == "." || filename == "/" {
		filename = "recovered_file_" + t.ID
	}
	outPath := filepath.Join(outDir, filename)
	if err := os.WriteFile(outPath, decompressed, 0644); err != nil {
		return fmt.Errorf("could not write output file: %w", err)
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
