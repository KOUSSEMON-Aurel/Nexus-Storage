package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type APIServer struct {
	db        *Database
	queue     *TaskQueue
	ytManager *YouTubeManager
}

func (s *APIServer) Start(port int) {
	mux := http.NewServeMux()
	mux.Handle("/webdav/", NewWebDAVHandler(s.db, s.queue))

	// File collection
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/files/", s.handleFileByID) // /api/files/{id}[/star|/restore|/permanent]

	// Uploads & downloads
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/download", s.handleDownload)

	// Trash
	mux.HandleFunc("/api/trash", s.handleTrash)

	// Background tasks & stats
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/security", s.handleSecurity)

	// Auth
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/mount", s.handleMount)

	handler := corsMiddleware(mux)
	fmt.Printf("🌐 API Server starting on http://localhost:%d\n", port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), handler))
}

// ─── /api/files ───────────────────────────────────────────────────────────────

func (s *APIServer) handleFiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		files, err := s.db.ListFiles()
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		if files == nil {
			files = []FileRecord{}
		}
		jsonOK(w, files)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleFileByID routes: /api/files/{id}, /api/files/{id}/star,
// /api/files/{id}/restore, /api/files/{id}/permanent
func (s *APIServer) handleFileByID(w http.ResponseWriter, r *http.Request) {
	// Strip prefix "/api/files/"
	rest := strings.TrimPrefix(r.URL.Path, "/api/files/")
	parts := strings.SplitN(rest, "/", 2)
	idStr := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	switch {
	// DELETE /api/files/{id}  → soft delete
	case action == "" && r.Method == http.MethodDelete:
		if err := s.db.SoftDelete(id); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})

	// POST /api/files/{id}/star
	case action == "star" && r.Method == http.MethodPost:
		var body struct {
			Starred bool `json:"starred"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if err := s.db.ToggleStar(id, body.Starred); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]bool{"starred": body.Starred})

	// POST /api/files/{id}/restore
	case action == "restore" && r.Method == http.MethodPost:
		if err := s.db.RestoreFile(id); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		jsonOK(w, map[string]string{"status": "restored"})

	// DELETE /api/files/{id}/permanent
	case action == "permanent" && r.Method == http.MethodDelete:
		fileRec, _ := s.db.GetFileByID(id)
		if err := s.db.PermanentDelete(id); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		if fileRec != nil && fileRec.VideoID != "" {
			s.queue.AddTask(&Task{
				ID:        fileRec.VideoID,
				Type:      TaskDelete,
				Status:    "Pending",
				CreatedAt: time.Now(),
			})
		}
		jsonOK(w, map[string]string{"status": "permanently_deleted"})

	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// ─── /api/trash ───────────────────────────────────────────────────────────────

func (s *APIServer) handleTrash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	files, err := s.db.ListTrash()
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if files == nil {
		files = []FileRecord{}
	}
	jsonOK(w, files)
}

// ─── /api/upload ──────────────────────────────────────────────────────────────

func (s *APIServer) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
		Mode string `json:"mode"` // "tank" | "density"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Mode == "" {
		req.Mode = "tank"
	}

	task := &Task{
		ID:        fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Type:      TaskUpload,
		FilePath:  req.Path,
		Mode:      req.Mode,
		Status:    "Pending",
		CreatedAt: time.Now(),
	}

	s.queue.AddTask(task)
	jsonOK(w, task)
}

// ─── /api/download ────────────────────────────────────────────────────────────

func (s *APIServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		VideoID string `json:"video_id"`
		Path    string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	task := &Task{
		ID:        req.VideoID,
		Type:      TaskDownload,
		FilePath:  req.Path,
		Status:    "Pending",
		CreatedAt: time.Now(),
	}
	s.queue.AddTask(task)
	jsonOK(w, task)
}

// ─── /api/tasks ───────────────────────────────────────────────────────────────

func (s *APIServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	s.queue.mu.Lock()
	defer s.queue.mu.Unlock()
	jsonOK(w, s.queue.tasks)
}

// ─── /api/stats ───────────────────────────────────────────────────────────────

func (s *APIServer) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stats, err := s.db.GetStats()
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	// Also append active task count
	s.queue.mu.Lock()
	active := 0
	for _, t := range s.queue.tasks {
		if t.Status != "Completed" && !strings.HasPrefix(t.Status, "Error") {
			active++
		}
	}
	s.queue.mu.Unlock()

	type extStats struct {
		Stats
		ActiveTasks int `json:"active_tasks"`
	}
	jsonOK(w, extStats{stats, active})
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

func (s *APIServer) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.ytManager == nil {
		jsonOK(w, map[string]any{"authenticated": false, "user": ""})
		return
	}
	s.ytManager.mu.RLock()
	defer s.ytManager.mu.RUnlock()
	jsonOK(w, map[string]any{
		"authenticated": s.ytManager.authed,
		"user":          s.ytManager.user,
	})
}

func (s *APIServer) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Run login in background to not block API
	go s.ytManager.StartLoginServer()
	jsonOK(w, map[string]string{"status": "login_flow_started", "url": s.ytManager.GetAuthURL()})
}

// ─── /api/security ─────────────────────────────────────────────────────────────

func (s *APIServer) handleSecurity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	// Return active security protocols to dynamically feed the frontend
	type Protocol struct {
		Name   string `json:"name"`
		Detail string `json:"detail"`
		Active bool   `json:"active"`
	}

	stats, _ := s.db.GetStats()

	protocols := []Protocol{
		{"XChaCha20-Poly1305 Encryption", fmt.Sprintf("%d files secured with unique keys", stats.FileCount), true},
		{"Argon2id Key Derivation", "64 MB memory, 3 passes — GPU resistant", true},
		{"SHA-256 + xxHash3 Integrity", "Dual fingerprint verification on every shard", true},
		{"Tank Pixel Encoding (4×4 B&W)", "High-resilience YouTube archival", true},
		{"Zero-Server Architecture", "Local private index, no central database", true},
	}
	jsonOK(w, protocols)
}

// ─── CORS middleware ──────────────────────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS, PROPFIND, MKCOL, MOVE, COPY, PROPPATCH, LOCK, UNLOCK")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Depth, If-Modified-Since, User-Agent, X-Expected-Entity-Length, Pragma, Cache-Control")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *APIServer) handleMount(w http.ResponseWriter, r *http.Request) {
	go autoMountLinux()
	jsonOK(w, map[string]string{"status": "mount-requested"})
}

func httpError(w http.ResponseWriter, err error, code int) {
	http.Error(w, err.Error(), code)
}
