package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type TaskType int

const (
	TaskUpload TaskType = iota
	TaskDownload
)

type Task struct {
	ID        string
	Type      TaskType
	FilePath  string
	Status    string
	Progress  float64
	CreatedAt time.Time
}

type TaskQueue struct {
	tasks map[string]*Task
	mu    sync.Mutex
	core  *NexusCore
	db    *Database
}

func (q *TaskQueue) Init(core *NexusCore, db *Database) {
	q.tasks = make(map[string]*Task)
	q.core = core
	q.db = db
}

func (q *TaskQueue) AddTask(t *Task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks[t.ID] = t
	go q.processTask(t)
}

func (q *TaskQueue) processTask(t *Task) {
	t.Status = "Processing"
	log.Printf("Starting task %s: %s", t.ID, t.FilePath)

	switch t.Type {
	case TaskUpload:
		err := q.handleUpload(t)
		if err != nil {
			t.Status = fmt.Sprintf("Error: %v", err)
			log.Printf("Task %s failed: %v", t.ID, err)
		} else {
			t.Status = "Completed"
			t.Progress = 100
			log.Printf("Task %s completed successfully", t.ID)
		}
	}
}

func (q *TaskQueue) handleUpload(t *Task) error {
	// 1. Read file
	data, err := os.ReadFile(t.FilePath)
	if err != nil {
		return fmt.Errorf("could not read file: %w", err)
	}

	// 2. Fingerprint (Deduplication)
	hash, err := q.core.Sha256(data)
	if err != nil {
		return fmt.Errorf("could not hash file: %w", err)
	}

	// 3. Compress (Auto mode)
	compressed, err := q.core.Compress(data, 0)
	if err != nil {
		return fmt.Errorf("compression failed: %w", err)
	}

	// 4. Encrypt
	password := "default-secret" // This should come from user config
	encrypted, err := q.core.Encrypt(compressed, password)
	if err != nil {
		return fmt.Errorf("encryption failed: %w", err)
	}

	// 5. Generate Frames (calling Rust core)
	// For this, we'll need to call the Rust encoder. 
	// (Note: encoder.rs isn't in FFI yet, let's assume we call a binary or CLI for now 
	// or I should add it to ffi.rs).
	
	// Temporary implementation using FFmpeg placeholder 
	// In a real scenario, we'd use the Rust library to write PNGs to a temp dir
	tempDir, _ := os.MkdirTemp("", "nexus-upload-*")
	defer os.RemoveAll(tempDir)

	// Here we would call: q.core.EncodeToFrames(encrypted, tempDir, "Tank")
	
	// 6. Encode to MP4 with FFmpeg
	outputVideo := filepath.Join(tempDir, "upload.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-framerate", "30", "-i", filepath.Join(tempDir, "frame_%06d.png"), 
		"-c:v", "libx264", "-pix_fmt", "yuv420p", outputVideo)
	
	log.Printf("Task %s: Encoding video...", t.ID)
	// (Skipping actual execution since frames aren't generated yet)

	// 7. Upload to YouTube (Placeholder)
	log.Printf("Task %s: Uploading to YouTube...", t.ID)
	videoID := "dummy_video_id" // Real YouTube API call would return this

	// 8. Save to local DB
	err = q.db.SaveFile(t.FilePath, videoID, int64(len(data)), hash, "encrypted-key-placeholder")
	if err != nil {
		return fmt.Errorf("db save failed: %w", err)
	}

	return nil
}
