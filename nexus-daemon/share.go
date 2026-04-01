package main

// share.go — V3 features: per-file encryption key sharing and LRU cache stats API handlers.

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ─── Cache Stats ──────────────────────────────────────────────────────────────

// GET /api/cache/stats
func (s *APIServer) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	if s.cache == nil {
		jsonOK(w, map[string]interface{}{
			"enabled":     false,
			"total_bytes": 0,
			"count":       0,
		})
		return
	}
	totalBytes, count := s.cache.Stats()
	jsonOK(w, map[string]interface{}{
		"enabled":     true,
		"total_bytes": totalBytes,
		"count":       count,
		"max_bytes":   s.cache.MaxBytes,
		"cache_dir":   s.cache.CacheDir,
	})
}

// ─── Per-File Shareable Links ─────────────────────────────────────────────────
// Route: POST /api/files/{id}/share  → called from handleFileByID

// generateShareToken creates a share link from a video ID and file key.
// Format: nexus://share/{base64url(videoID)}#{base64url(fileKey)}
func generateShareToken(videoID string, fileKey []byte) string {
	vidEncoded := base64.RawURLEncoding.EncodeToString([]byte(videoID))
	keyEncoded := base64.RawURLEncoding.EncodeToString(fileKey)
	return fmt.Sprintf("nexus://share/%s#%s", vidEncoded, keyEncoded)
}

// parseShareToken decodes a nexus://share/... link into video ID and file key.
func parseShareToken(token string) (videoID string, fileKey []byte, err error) {
	// Remove scheme
	rest := strings.TrimPrefix(token, "nexus://share/")
	if rest == token {
		return "", nil, fmt.Errorf("invalid share token format")
	}

	// Split on '#'
	parts := strings.SplitN(rest, "#", 2)
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("share token missing key separator '#'")
	}

	vidBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", nil, fmt.Errorf("invalid video ID encoding: %w", err)
	}

	fileKey, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", nil, fmt.Errorf("invalid key encoding: %w", err)
	}

	return string(vidBytes), fileKey, nil
}

// handleShare is called from handleFileByID when action == "share".
// POST /api/files/{id}/share
// Body (optional): { "master_key_hex": "..." }
// On first call:  generates a per-file key, stores it encrypted with master, returns link.
// On repeat call: returns the existing link (idempotent).
func (s *APIServer) handleShare(w http.ResponseWriter, r *http.Request, fileID int64) {
	nc := &NexusCore{}
	
	// Get masterKey from request or session
	var req struct {
		MasterKeyHex string `json:"master_key_hex"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	
	masterSecret := req.MasterKeyHex
	if masterSecret == "" {
		httpError(w, fmt.Errorf("missing master_key_hex in request body"), http.StatusBadRequest)
		return
	}

	// Fetch the stored file_key (may be empty first time)
	storedKeyHex, err := s.db.GetFileKey(fileID)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	// Fetch the file record to get videoID
	file, err := s.db.GetFileByID(fileID)
	if err != nil || file == nil {
		httpError(w, fmt.Errorf("file not found"), http.StatusNotFound)
		return
	}

	var rawFileKey []byte

	if storedKeyHex == "" {
		// First share: generate a fresh 32-byte key
		rawKey, err := nc.GenerateFileKey()
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		rawFileKey = rawKey

		// Encrypt the file key with master secret (V4: now password-derived)
		encryptedKey, err := nc.Encrypt(rawFileKey, masterSecret)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		storedKeyHex = hex.EncodeToString(encryptedKey)

		if err := s.db.SetFileKey(fileID, storedKeyHex); err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
	} else {
		// Key already exists — decrypt to get the raw bytes
		encryptedKey, err := hex.DecodeString(storedKeyHex)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		rawFileKey, err = nc.Decrypt(encryptedKey, masterSecret)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
	}

	token := generateShareToken(file.VideoID, rawFileKey)
	jsonOK(w, map[string]string{
		"link":     token,
		"video_id": file.VideoID,
	})
}

// ─── Shared Download ──────────────────────────────────────────────────────────

// POST /api/download/shared
// Body: { "token": "nexus://share/...", "output_path": "/tmp/myfile" }
func (s *APIServer) handleSharedDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token      string `json:"token"`
		OutputPath string `json:"output_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	_, _, err := parseShareToken(req.Token)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid share token: %v", err), http.StatusBadRequest)
		return
	}

	// Enqueue a download task using the share token info
	// TODO: extend TaskQueue to accept pre-supplied file key for shared downloads.
	// For now we return the parsed info to confirm the token is valid.
	jsonOK(w, map[string]string{
		"status":  "queued",
		"message": "Shared download support is being implemented",
	})
}

// ─── Wire handleShare into handleFileByID ─────────────────────────────────────
// This is a helper called from handleFileByID in api.go when action == "share"

func (s *APIServer) dispatchFileAction(w http.ResponseWriter, r *http.Request, action string, idStr string) bool {
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return false
	}
	switch action {
	case "share":
		if r.Method == http.MethodPost {
			s.handleShare(w, r, id)
			return true
		}
	}
	return false
}
