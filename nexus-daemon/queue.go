package main

import (
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
	ID        string
	Type      TaskType
	FilePath  string
	Mode      string
	Status    string
	Progress  float64
	CreatedAt time.Time
}

type TaskQueue struct {
	tasks     map[string]*Task
	mu        sync.Mutex
	core      *NexusCore
	db        *Database
	ytManager *YouTubeManager
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

func (q *TaskQueue) Init(core *NexusCore, db *Database, ytManager *YouTubeManager) {
	q.tasks = make(map[string]*Task)
	q.core = core
	q.db = db
	q.ytManager = ytManager
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
	t.Status = "Reading file"
	t.Progress = 5
	data, err := os.ReadFile(t.FilePath)
	if err != nil {
		return fmt.Errorf("could not read file: %w", err)
	}

	t.Status = "Hashing"
	hash, err := q.core.Sha256(data)
    if err != nil {
		return fmt.Errorf("sha256 failed: %w", err)
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
	log.Printf("Generated %d frames in %s", frameCount, tempDir)

	t.Status = "FFmpeg: MP4"
	t.Progress = 50
	outputVideo := filepath.Join(tempDir, "upload.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-framerate", "30", "-i", filepath.Join(tempDir, "frame_%06d.png"),
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-crf", "18", outputVideo)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg MP4 creation failed: %w", err)
	}

	t.Status = "YouTube: Uploading"
	t.Progress = 70
	if q.ytManager == nil || !q.ytManager.IsAuthenticated() {
		log.Println("⚠️  YouTube service offline. File saved locally only.")
		t.Status = "Saved (Local Only - No YouTube Auth)"
		return q.db.SaveFile(filepath.Base(t.FilePath), "local-"+hash[:8], int64(len(data)), hash, "mock-key")
	}

	ytService := q.ytManager.GetService()
	uploadFile, err := os.Open(outputVideo)
	if err != nil {
		return fmt.Errorf("could not open generated MP4: %w", err)
	}
	defer uploadFile.Close()

	upload := &youtube.Video{
		Snippet: &youtube.VideoSnippet{
			Title:       "NexusStorage Shard - " + filepath.Base(t.FilePath),
			Description: "Encrypted data shard. Hash: " + hash,
			CategoryId:  "22",
		},
		Status: &youtube.VideoStatus{PrivacyStatus: "private"},
	}

	call := ytService.Videos.Insert([]string{"snippet", "status"}, upload)
	response, err := call.Media(uploadFile).Do()
	if err != nil {
		log.Printf("⚠️  YouTube API error: %v", err)
		log.Println("💡 If you see 'forbidden', add your email as Test User in Google Cloud Console > OAuth Consent Screen.")
		t.Status = "Error: YouTube API - check daemon logs"
		// Still save locally so user doesn't lose their work
		q.db.SaveFile(filepath.Base(t.FilePath), "local-"+hash[:8], int64(len(data)), hash, "mock-key")
		return fmt.Errorf("YouTube upload failed (file saved locally): %w", err)
	}

	t.Status = "Finalizing"
	t.Progress = 95
	return q.db.SaveFile(filepath.Base(t.FilePath), response.Id, int64(len(data)), hash, "encrypted-key")
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
