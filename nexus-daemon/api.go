package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type APIServer struct {
	db             *Database
	queue          *TaskQueue
	ytManager      *YouTubeManager
	pm             *PlaylistManager
	cache          *CacheManager
	syncMu         sync.Mutex
	syncInProgress bool
	// Quota cache to avoid spamming Google Cloud Monitoring API
	quotaCache     int
	quotaCacheTime time.Time
	quotaCacheMu   sync.Mutex
}

func (s *APIServer) Start(port int) {
	mux := http.NewServeMux()
	mux.Handle("/vfs/", NewVFSHandler(s.db, s.queue))

	// File collection
	mux.HandleFunc("/api/files", s.handleFiles)
	mux.HandleFunc("/api/files/", s.handleFileByID) // /api/files/{id}[/star|/restore|/permanent]
	mux.HandleFunc("/api/search", s.handleSearch)

	// Uploads & downloads
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/download", s.handleDownload)

	// Trash
	mux.HandleFunc("/api/trash", s.handleTrash)

	// Background tasks & stats
	mux.HandleFunc("/api/tasks", s.handleTasks)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/security", s.handleSecurity)

	// Auth & Quota
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	// V4 Security
	mux.HandleFunc("/api/auth/session-start", s.handleSessionStart)
	mux.HandleFunc("/api/auth/session-end", s.handleSessionEnd)
	mux.HandleFunc("/api/auth/password-change", s.handlePasswordChange)
	// V4 Recovery
	mux.HandleFunc("/api/recovery/backup", s.handleRecoveryBackup)
	mux.HandleFunc("/api/recovery/restore", s.handleRecoveryRestore)
	
	mux.HandleFunc("/api/quota", s.handleQuota)
	mux.HandleFunc("/api/quota/live", s.handleQuotaLiveToggle)
	mux.HandleFunc("/api/quota/limit", s.handleQuotaLimit)
	mux.HandleFunc("/api/cloud/sync", s.handleCloudSync)
	mux.HandleFunc("/api/mount", s.handleMount)
	mux.HandleFunc("/api/unmount", s.handleUnmount)
	mux.HandleFunc("/api/mount/status", s.handleMountStatus)
	mux.HandleFunc("/api/studio", s.handleStudio)

	// V3: Cache stats and Shared Links
	mux.HandleFunc("/api/cache/stats", s.handleCacheStats)
	mux.HandleFunc("/api/download/shared", s.handleSharedDownload)
	mux.HandleFunc("/api/settings/trash", s.handleTrashSettings)

	handler := corsMiddleware(mux)
	fmt.Printf("🌐 API Server starting on http://localhost:%d\n", port)
	
	// Pre-warm quota cache after a delay to ensure YouTube auth is ready
	go func() {
		// Wait 3 seconds for auth to complete and scope validation
		time.Sleep(3 * time.Second)
		if liveUsed, hasLive := s.ytManager.GetLiveQuota(); hasLive {
			s.quotaCacheMu.Lock()
			s.quotaCache = liveUsed
			s.quotaCacheTime = time.Now()
			s.quotaCacheMu.Unlock()
			log.Printf("✅ Quota cache pre-warmed with %d units from live monitoring", liveUsed)
		} else {
			log.Printf("⚠️  Quota cache pre-warm: live monitoring not available")
		}
	}()
	
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
		s.queue.RequestManifestBackup()
		jsonOK(w, map[string]string{"status": "deleted"})
		
	// POST /api/files/{id}/evict -> "Free up space" (clear from local cache but keep in DB)
	case action == "evict" && r.Method == http.MethodPost:
		f, _ := s.db.GetFileByID(id)
		if f != nil {
			path := filepath.Base(f.Path)
			currParent := f.ParentID
			for currParent != nil {
				folder, _ := s.db.GetFolderByID(*currParent)
				if folder == nil { break }
				path = filepath.Join(folder.Name, path)
				currParent = folder.ParentID
			}
			home, _ := os.UserHomeDir()
			cachePath := filepath.Join(home, ".nexus", "cache", path)
			os.Remove(cachePath)
		}
		jsonOK(w, map[string]string{"status": "evicted"})

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
		s.queue.RequestManifestBackup()
		jsonOK(w, map[string]bool{"starred": body.Starred})

	// POST /api/files/{id}/restore
	case action == "restore" && r.Method == http.MethodPost:
		if err := s.db.Restore(id); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		s.queue.RequestManifestBackup()
		jsonOK(w, map[string]string{"status": "restored"})

	// DELETE /api/files/{id}/permanent
	case action == "permanent" && r.Method == http.MethodDelete:
		fileRec, _ := s.db.GetFileByID(id)
		if err := s.db.PermanentDelete(id); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		s.queue.RequestManifestBackup()
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
		if !s.dispatchFileAction(w, r, action, idStr) {
			http.Error(w, "not found", http.StatusNotFound)
		}
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
		Path     string `json:"path"`
		Mode     string `json:"mode"` // "tank" | "density"
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Mode == "" || req.Mode == "tank" {
		req.Mode = "base"
	}
	if req.Mode == "density" {
		req.Mode = "high"
	}

	// Basic validation
	if _, err := os.Stat(req.Path); os.IsNotExist(err) {
		http.Error(w, "file or folder does not exist", http.StatusBadRequest)
		return
	}

	task := &Task{
		ID:        fmt.Sprintf("task-%d", time.Now().UnixNano()),
		Type:      TaskUpload,
		FilePath:  req.Path,
		Mode:      req.Mode,
		Password:  req.Password,
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
		VideoID  string `json:"video_id"`
		Path     string `json:"path"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	task := &Task{
		ID:        req.VideoID,
		Type:      TaskDownload,
		FilePath:  req.Path,
		Password:  req.Password,
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
	
	url := s.ytManager.GetAuthURL()
	go openBrowser(url)
	
	jsonOK(w, map[string]string{"status": "login_flow_started", "url": url})
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
			// Do not intercept OPTIONS for vfs so the net/vfs
			// handler can inject DAV: 1, 2 and Allow capabilities.
			if !strings.HasPrefix(r.URL.Path, "/vfs/") && !strings.HasPrefix(r.URL.Path, "/vfs") {
				w.WriteHeader(http.StatusOK)
				return
			}
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
	go autoMountVirtualDisk()
	jsonOK(w, map[string]string{"status": "mount-requested"})
}

func (s *APIServer) handleUnmount(w http.ResponseWriter, r *http.Request) {
	unmountVirtualDisk()
	jsonOK(w, map[string]string{"status": "unmount-requested"})
}

func (s *APIServer) handleMountStatus(w http.ResponseWriter, r *http.Request) {
	mounted := isVirtualDiskMounted()
	jsonOK(w, map[string]bool{"mounted": mounted})
}

func (s *APIServer) handleStudio(w http.ResponseWriter, r *http.Request) {
	channelID := s.ytManager.GetChannelID()
	url := "https://studio.youtube.com/videos/upload" // Fallback

	if channelID != "" {
		// Exact working format provided by the user
		url = fmt.Sprintf("https://studio.youtube.com/channel/%s/videos/upload?filter=%%5B%%5D&sort=%%7B%%22columnType%%22%%3A%%22date%%22%%2C%%22sortOrder%%22%%3A%%22DESCENDING%%22%%7D", channelID)
	}
	
	go openBrowser(url)
	jsonOK(w, map[string]string{"status": "browser-launched", "url": url})
}

func (s *APIServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		s.handleFiles(w, r)
		return
	}
	files, err := s.db.SearchFiles(query)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	jsonOK(w, files)
}

func (s *APIServer) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.ytManager.Logout()
	jsonOK(w, map[string]any{"status": "logged_out"})
}

// ─── V4 Security: Session Management ──────────────────────────────────────────

// POST /api/auth/session-start
// Body: { "master_key_hex": "..." }
// Stores masterKey in RAM (TaskQueue), valid until logout/shutdown
func (s *APIServer) handleSessionStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MasterKeyHex string `json:"master_key_hex"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("invalid request: %w", err), http.StatusBadRequest)
		return
	}

	if len(req.MasterKeyHex) != 64 { // 32 bytes * 2 hex chars
		httpError(w, fmt.Errorf("invalid master_key_hex: expected 64 hex characters, got %d", len(req.MasterKeyHex)), http.StatusBadRequest)
		return
	}

	// Validate it's valid hex
	if _, err := hex.DecodeString(req.MasterKeyHex); err != nil {
		httpError(w, fmt.Errorf("master_key_hex is not valid hex: %w", err), http.StatusBadRequest)
		return
	}

	// Store in queue (RAM-only)
	s.queue.SetMasterKeyHex(req.MasterKeyHex)

	jsonOK(w, map[string]any{
		"status": "session_active",
		"message": "Master key loaded into session. Valid until logout.",
	})
}

// POST /api/auth/session-end
// Clears masterKey from RAM
func (s *APIServer) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.queue.ClearMasterKeyHex()

	jsonOK(w, map[string]any{
		"status": "session_ended",
		"message": "Master key cleared from memory.",
	})
}

// POST /api/auth/password-change
// V4.1: Change password and re-encrypt all file_keys
// Body: { "old_password": "...", "new_password": "..." }
// Returns: { "status": "success", "files_rotated": N, "new_revision": N }
func (s *APIServer) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("invalid request: %w", err), http.StatusBadRequest)
		return
	}

	// Validate inputs
	if req.OldPassword == "" {
		httpError(w, fmt.Errorf("old_password required"), http.StatusBadRequest)
		return
	}
	if req.NewPassword == "" {
		httpError(w, fmt.Errorf("new_password required"), http.StatusBadRequest)
		return
	}
	if req.OldPassword == req.NewPassword {
		httpError(w, fmt.Errorf("new password must differ from old password"), http.StatusBadRequest)
		return
	}

	// Perform password rotation
	count, newRev, err := s.queue.RotatePassword(req.OldPassword, req.NewPassword)
	if err != nil {
		httpError(w, fmt.Errorf("password rotation failed: %w", err), http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"status": "success",
		"files_rotated": count,
		"new_revision": newRev,
		"message": fmt.Sprintf("✅ Password changed successfully. %d files re-encrypted. Manifest backed up.", count),
	})
}

// ─── V4 Recovery: Manifest Backup/Restore ────────────────────────────────────

// POST /api/recovery/backup
// Triggers immediate backup of encrypted manifest to Drive
// Body (optional): { "master_key_hex": "..." }
func (s *APIServer) handleRecoveryBackup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MasterKeyHex string `json:"master_key_hex"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	masterKeyHex := req.MasterKeyHex
	if masterKeyHex == "" {
		// Try to use session masterKey
		masterKeyHex = s.queue.GetMasterKeyHex()
	}

	if masterKeyHex == "" {
		httpError(w, fmt.Errorf("no master key available: provide in request or call /api/auth/session-start first"), http.StatusUnauthorized)
		return
	}

	// Trigger backup (async via queue)
	err := s.queue.EncryptAndBackupManifest(masterKeyHex)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"status": "backup_queued",
		"message": "Manifest backup initiated. Check Drive shortly.",
	})
}

// POST /api/recovery/restore
// Downloads and decrypts manifest from Drive, restores to DB
// Body: { "master_key_hex": "..." }
func (s *APIServer) handleRecoveryRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MasterKeyHex string `json:"master_key_hex"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("invalid request: %w", err), http.StatusBadRequest)
		return
	}

	if req.MasterKeyHex == "" {
		httpError(w, fmt.Errorf("missing master_key_hex"), http.StatusBadRequest)
		return
	}

	// Download & decrypt manifest
	manifest, err := s.queue.RestoreManifestFromDrive(req.MasterKeyHex)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	// Apply to DB
	if err := s.queue.ApplyRestoredManifestToDB(manifest); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"status": "restored",
		"file_count": len(manifest.Files),
		"message": fmt.Sprintf("Restored %d files from Drive backup", len(manifest.Files)),
	})
}

func (s *APIServer) handleQuota(w http.ResponseWriter, r *http.Request) {
	// Check for force parameter
	forceLive := r.URL.Query().Get("force") == "true"
	
	used := s.db.GetDailyQuota()
	limitStr, ok := s.db.GetKV("quota_limit")
	limit := 10000
	if ok {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
		}
	}

	// Check if live monitoring is enabled (default: true, can be disabled to save API calls)
	enableLiveQuota := true
	if val, ok := s.db.GetKV("enable_live_quota"); ok && val == "false" {
		enableLiveQuota = false
	}
	
	source := "local"
	
	// Check cache - only call live quota if cache is older than 5 minutes and enabled
	s.quotaCacheMu.Lock()
	cacheValid := enableLiveQuota && !forceLive && time.Since(s.quotaCacheTime) < 5*time.Minute && s.quotaCacheTime.After(time.Now().Add(-24*time.Hour)) && s.quotaCacheTime.After(time.Time{})
	if cacheValid {
		used = s.quotaCache
		source = "cached"
	}
	s.quotaCacheMu.Unlock()
	
	// Try real-time monitoring if not using valid cache and enabled
	if !cacheValid && enableLiveQuota {
		liveUsed, hasLive := s.ytManager.GetLiveQuota()
		if hasLive {
			used = liveUsed
			source = "monitoring"
			// Update cache
			s.quotaCacheMu.Lock()
			s.quotaCache = liveUsed
			s.quotaCacheTime = time.Now()
			s.quotaCacheMu.Unlock()
		}
	}

	jsonOK(w, map[string]any{
		"used":   used,
		"limit":  limit,
		"source": source,
	})
}

func (s *APIServer) handleQuotaLiveToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Get current status
		enabled := true
		if val, ok := s.db.GetKV("enable_live_quota"); ok && val == "false" {
			enabled = false
		}
		jsonOK(w, map[string]any{"enabled": enabled})
		return
	}
	
	if r.Method == http.MethodPost {
		// Toggle status
		current := "true"
		if val, ok := s.db.GetKV("enable_live_quota"); ok && val == "false" {
			current = "false"
		}
		
		newValue := "false"
		if current == "false" {
			newValue = "true"
		}
		
		if err := s.db.SetKV("enable_live_quota", newValue); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		
		// Clear cache when disabling
		if newValue == "false" {
			s.quotaCacheMu.Lock()
			s.quotaCache = 0
			s.quotaCacheTime = time.Time{}
			s.quotaCacheMu.Unlock()
		}
		
		jsonOK(w, map[string]any{"enabled": newValue == "true"})
		return
	}
	
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *APIServer) handleCloudSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.syncMu.Lock()
	if s.syncInProgress {
		s.syncMu.Unlock()
		http.Error(w, "Sync already in progress", http.StatusConflict)
		return
	}
	s.syncInProgress = true
	s.syncMu.Unlock()

	defer func() {
		s.syncMu.Lock()
		s.syncInProgress = false
		s.syncMu.Unlock()
	}()

	log.Printf("🔄 Manual cloud sync requested via API...")
	if err := s.pm.DownloadLatestManifest(); err != nil {
		log.Printf("❌ Cloud sync failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("✅ Manual cloud sync completed.")
	jsonOK(w, map[string]any{"status": "ok", "message": "Manifest sync completed"})
}

func (s *APIServer) handleQuotaLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Limit int `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.db.SetKV("quota_limit", fmt.Sprintf("%d", req.Limit))
	jsonOK(w, map[string]any{"status": "ok", "limit": req.Limit})
}


func (s *APIServer) handleTrashSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		days := 30
		if d, ok := s.db.GetKV("trash_purge_days"); ok {
			fmt.Sscanf(d, "%d", &days)
		}
		jsonOK(w, map[string]int{"purge_days": days})
		return
	}
	if r.Method == http.MethodPost {
		var req struct {
			Days int `json:"purge_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.db.SetKV("trash_purge_days", fmt.Sprintf("%d", req.Days))
		jsonOK(w, map[string]any{"status": "ok", "purge_days": req.Days})
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func httpError(w http.ResponseWriter, err error, code int) {
	http.Error(w, err.Error(), code)
}
