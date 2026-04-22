package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"io"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

type YouTubeManager struct {
	config       *oauth2.Config
	service      *youtube.Service
	driveService *drive.Service
	mu           sync.RWMutex
	authed       bool
	user         string
	channelID    string
	googleSub    string // ← Unique, permanent Google user ID
}

func getConfigDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	path := filepath.Join(dir, "nexus-storage")
	os.MkdirAll(path, 0755)
	return path
}

func NewYouTubeManager() *YouTubeManager {
	m := &YouTubeManager{}

	var b []byte
	var err error
	b, err = os.ReadFile(filepath.Join(getConfigDir(), "client_secret.json"))
	if err != nil {
		// Fallback to local directory (for development)
		b, err = os.ReadFile("client_secret.json")
	}
	if err != nil {
		log.Printf("⚠️  YouTube: client_secret.json not found. Authentication disabled. Error: %v", err)
		return m // Return unauthenticated manager, never nil
	}

	// Pro Scope: YoutubeForceSslScope for playlist management + Monitoring for real-time quota + Drive for manifest + OpenID for user identity
	config, err := google.ConfigFromJSON(b,
		"https://www.googleapis.com/auth/youtube.force-ssl",
		"https://www.googleapis.com/auth/monitoring.read",
		drive.DriveScope,
		"openid", // ← REQUIRED for id_token (contains 'sub' claim)
	)
	if err != nil {
		log.Printf("⚠️  YouTube: could not parse client_secret.json: %v", err)
		return m
	}
	config.RedirectURL = "http://localhost:8080"
	m.config = config
	m.TryLoadToken()
	return m
}

func (m *YouTubeManager) TryLoadToken() bool {
	tokFile := filepath.Join(getConfigDir(), "token.json")
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		return false
	}

	if m.config == nil {
		return false
	}

	client := m.config.Client(context.Background(), tok)
	service, err := youtube.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return false
	}
	driveService, err := drive.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		log.Printf("⚠️  Drive: could not create service: %v", err)
	}

	// Load Google sub from separate file (where we saved it from UserInfo API)
	subFile := filepath.Join(getConfigDir(), "google-sub.txt")
	subBytes, err := os.ReadFile(subFile)
	googleSub := ""
	if err == nil {
		googleSub = string(subBytes)
	}

	if googleSub != "" {
		log.Printf("✅ Google sub loaded: %s (auto-encryption enabled)", googleSub[:8]+"...")
	} else {
		log.Printf("❌ CRITICAL ERROR: Google sub file not found!")
		log.Printf("   Old token is incompatible. Removing token and forcing re-authentication...")
		os.Remove(filepath.Join(getConfigDir(), "token.json"))
		os.Remove(filepath.Join(getConfigDir(), "google-sub.txt"))
		return false // Token is invalid - do NOT mark as authenticated
	}

	m.mu.Lock()
	m.service = service
	m.driveService = driveService
	m.authed = true
	m.googleSub = googleSub
	m.mu.Unlock()

	// Signal UI to refresh
	select {
	case AuthNotify <- true:
	default:
	}

	// Async fetch channel info
	go m.FetchChannelID()

	return true
}

func (m *YouTubeManager) Logout() {
	m.mu.Lock()
	m.service = nil
	m.driveService = nil
	m.authed = false
	m.user = ""
	m.channelID = ""
	m.googleSub = ""
	m.mu.Unlock()
	os.Remove(filepath.Join(getConfigDir(), "token.json"))
}

func (m *YouTubeManager) IsAuthenticated() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.authed
}

func (m *YouTubeManager) GetAuthStatus() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.authed {
		return "Offline (Login Required)"
	}
	return "Online (" + m.user + ")"
}

func (m *YouTubeManager) GetService() *youtube.Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.service
}

func (m *YouTubeManager) GetGoogleSub() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.googleSub
}

func (m *YouTubeManager) FetchChannelID() {
	svc := m.GetService()
	if svc == nil {
		return
	}

	call := svc.Channels.List([]string{"id", "snippet"}).Mine(true)
	resp, err := call.Do()
	if err != nil {
		log.Printf("⚠️  YouTube: Failed to fetch channel ID: %v", err)
		return
	}

	if len(resp.Items) > 0 {
		m.mu.Lock()
		m.channelID = resp.Items[0].Id
		m.user = resp.Items[0].Snippet.Title
		m.mu.Unlock()
		log.Printf("👤 YouTube Authenticated: %s (%s)", m.user, m.channelID)
	}
}

func (m *YouTubeManager) GetLiveQuota() (int, bool) {
	m.mu.RLock()
	authed := m.authed
	config := m.config
	m.mu.RUnlock()

	if !authed || config == nil {
		return 0, false
	}

	tokFile := filepath.Join(getConfigDir(), "token.json")
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		return 0, false
	}

	client := config.Client(context.Background(), tok)

	// Extract project_id from client_secret
	b, _ := os.ReadFile(filepath.Join(getConfigDir(), "client_secret.json"))
	if len(b) == 0 {
		b, _ = os.ReadFile("client_secret.json")
	}
	var secret struct {
		Installed struct {
			ProjectID string `json:"project_id"`
		} `json:"installed"`
	}
	json.Unmarshal(b, &secret)
	projectID := secret.Installed.ProjectID
	if projectID == "" {
		log.Printf("⚠️  GetLiveQuota: project_id not found in client_secret.json")
		return 0, false
	}

	// Prepare time interval (current PT day)
	pt, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		log.Printf("⚠️  GetLiveQuota: could not load PT timezone: %v", err)
		return 0, false
	}
	now := time.Now().In(pt)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, pt).UTC()
	end := now.UTC()

	filter := `metric.type="serviceruntime.googleapis.com/quota/rate/net_usage" AND resource.labels.service="youtube.googleapis.com"`
	url := fmt.Sprintf("https://monitoring.googleapis.com/v3/projects/%s/timeSeries?filter=%s&interval.startTime=%s&interval.endTime=%s",
		projectID, strings.ReplaceAll(filter, " ", "%20"), start.Format(time.RFC3339), end.Format(time.RFC3339))

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("⚠️  GetLiveQuota: HTTP error: %v", err)
		return 0, false
	}
	if resp.StatusCode != 200 {
		log.Printf("⚠️  GetLiveQuota: HTTP status %d", resp.StatusCode)
		return 0, false
	}
	defer resp.Body.Close()

	var monitorResp struct {
		TimeSeries []struct {
			Points []struct {
				Value struct {
					Int64Value string `json:"int64Value"`
				} `json:"value"`
			} `json:"points"`
		} `json:"timeSeries"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&monitorResp); err != nil {
		return 0, false
	}

	total := 0
	for _, ts := range monitorResp.TimeSeries {
		for _, p := range ts.Points {
			var val int
			fmt.Sscanf(p.Value.Int64Value, "%d", &val)
			total += val
		}
	}

	log.Printf("✅ GetLiveQuota: Retrieved %d units from monitoring API", total)
	return total, true
}

func (m *YouTubeManager) VideoExists(videoID string) (bool, error) {
	driveSvc := m.GetDriveService() // First try a quick check if it's treated as a file (Nexus 2.0)
	if driveSvc != nil {
		_, err := driveSvc.Files.Get(videoID).Fields("id").Do()
		if err == nil {
			return true, nil
		}
	}

	ytService := m.GetService()
	if ytService == nil {
		return false, fmt.Errorf("youtube service not initialized")
	}

	call := ytService.Videos.List([]string{"id"}).Id(videoID)
	resp, err := call.Do()
	if err != nil {
		return false, err
	}
	return len(resp.Items) > 0, nil
}

func (m *YouTubeManager) GetChannelID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.channelID
}

func (m *YouTubeManager) getMetadataFolderID() (string, error) {
	driveSvc := m.GetDriveService()
	if driveSvc == nil {
		return "", fmt.Errorf("drive service not initialized")
	}

	query := "name = 'Nexus-Storage-Metadata' and mimeType = 'application/vnd.google-apps.folder' and trashed = false"
	list, err := driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err == nil && len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	// Create it if not found
	f := &drive.File{
		Name:     "Nexus-Storage-Metadata",
		MimeType: "application/vnd.google-apps.folder",
	}
	res, err := driveSvc.Files.Create(f).Do()
	if err != nil {
		return "", err
	}
	return res.Id, nil
}

func (m *YouTubeManager) UploadManifestToDrive(filename string, data io.Reader) (string, error) {
	driveSvc := m.GetDriveService()
	if driveSvc == nil {
		return "", fmt.Errorf("drive service not initialized")
	}

	folderID, _ := m.getMetadataFolderID()

	// 1. Search for existing manifest file in that folder to overwrite
	query := "name = 'nexus.db' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'nexus.db' and '%s' in parents and trashed = false", folderID)
	}

	fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err == nil && len(fileList.Files) > 0 {
		fileID := fileList.Files[0].Id
		// Overwrite existing
		_, err = driveSvc.Files.Update(fileID, nil).Media(data).Do()
		return fileID, err
	}

	// 2. Create new if not found
	f := &drive.File{
		Name:     "nexus.db",
		MimeType: "application/x-sqlite3",
	}
	if folderID != "" {
		f.Parents = []string{folderID}
	}
	res, err := driveSvc.Files.Create(f).Media(data).Do()
	if err != nil {
		return "", err
	}
	return res.Id, nil
}

func (m *YouTubeManager) DownloadManifestFromDrive() (io.ReadCloser, error) {
	driveSvc := m.GetDriveService()
	if driveSvc == nil {
		return nil, fmt.Errorf("drive service not initialized")
	}

	folderID, _ := m.getMetadataFolderID()
	query := "name = 'nexus.db' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'nexus.db' and '%s' in parents and trashed = false", folderID)
	}

	fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err != nil || len(fileList.Files) == 0 {
		return nil, fmt.Errorf("manifest not found on drive")
	}

	fileID := fileList.Files[0].Id
	resp, err := driveSvc.Files.Get(fileID).Download()
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (m *YouTubeManager) GetDriveService() *drive.Service {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.driveService
}

func (m *YouTubeManager) GetAuthURL() string {
	return m.config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
}

func (m *YouTubeManager) StartLoginServer() error {

	srv := &http.Server{Addr: ":8080"}
	handler := http.NewServeMux()

	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code != "" {
			tok, err := m.config.Exchange(context.TODO(), code)
			if err != nil {
				fmt.Fprintf(w, "Auth exchange failed: %v", err)
				return
			}

			// CRITICAL: Fetch user info with access_token to get Google 'sub' directly
			// (oauth2-go doesn't automatically return id_token even with openid scope)
			var googleSub string
			resp, err := (&http.Client{}).Get("https://www.googleapis.com/oauth2/v2/userinfo?access_token=" + tok.AccessToken)
			if err != nil {
				log.Printf("❌ Failed to fetch Google userinfo: %v", err)
			} else {
				defer resp.Body.Close()
				var userInfo map[string]interface{}
				json.NewDecoder(resp.Body).Decode(&userInfo)
				if id, ok := userInfo["id"].(string); ok {
					googleSub = id
					log.Printf("✅ Google sub fetched via UserInfo API: %s", id[:8]+"...")
				}
			}

			saveToken(filepath.Join(getConfigDir(), "token.json"), tok)

			// Save Google sub separately (oauth2-go can't serialize id_token properly)
			if googleSub != "" {
				os.WriteFile(filepath.Join(getConfigDir(), "google-sub.txt"), []byte(googleSub), 0600)
			}

			m.TryLoadToken()

			html := `
			<!DOCTYPE html>
			<html lang="en">
			<head>
				<meta charset="UTF-8">
				<title>Nexus Storage - Authenticated</title>
				<meta name="viewport" content="width=device-width, initial-scale=1.0">
				<link rel="preconnect" href="https://fonts.googleapis.com">
				<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
				<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;600;700;800&display=swap" rel="stylesheet">
				<style>
					:root { 
						--primary: #6366f1; 
						--success: #10b981;
						--bg: #030712; 
						--card: #111827;
						--text: #f9fafb;
						--text-muted: #9ca3af;
					}
					* { box-sizing: border-box; }
					body { 
						background: var(--bg);
						background-image: 
							radial-gradient(at 0% 0%, hsla(253,16%,7%,1) 0, transparent 50%), 
							radial-gradient(at 50% 0%, hsla(225,39%,30%,0.15) 0, transparent 50%),
							radial-gradient(at 100% 0%, hsla(339,49%,30%,0.15) 0, transparent 50%);
						color: var(--text); 
						font-family: 'Inter', -apple-system, sans-serif;
						display: flex; justify-content: center; align-items: center; 
						height: 100vh; margin: 0; overflow: hidden;
					}
					.card {
						background: var(--card);
						padding: 64px 48px;
						border-radius: 40px;
						border: 1px solid rgba(255, 255, 255, 0.05);
						box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
						text-align: center;
						max-width: 480px;
						width: 90%;
						position: relative;
						animation: fadeIn 0.8s cubic-bezier(0.16, 1, 0.3, 1);
					}
					@keyframes fadeIn {
						from { opacity: 0; transform: translateY(30px) scale(0.95); }
						to { opacity: 1; transform: translateY(0) scale(1); }
					}
					.icon-container {
						width: 100px; height: 100px;
						background: rgba(16, 185, 129, 0.1);
						border-radius: 30px;
						display: flex; justify-content: center; align-items: center;
						margin: 0 auto 40px;
						position: relative;
					}
					.icon-container::after {
						content: '';
						position: absolute;
						inset: -10px;
						border: 2px solid rgba(16, 185, 129, 0.2);
						border-radius: 40px;
						animation: pulse 2s infinite;
					}
					@keyframes pulse {
						0% { transform: scale(1); opacity: 0.5; }
						50% { transform: scale(1.05); opacity: 0.2; }
						100% { transform: scale(1); opacity: 0.5; }
					}
					svg { width: 48px; height: 48px; color: var(--success); }
					h1 { 
						margin: 0 0 16px; 
						font-size: 32px; 
						font-weight: 800; 
						letter-spacing: -0.025em;
						background: linear-gradient(to bottom right, #fff, #9ca3af);
						-webkit-background-clip: text;
						-webkit-text-fill-color: transparent;
					}
					p { 
						color: var(--text-muted); 
						font-size: 17px; 
						line-height: 1.6; 
						margin-bottom: 0; 
						font-weight: 400; 
					}
					.footer {
						margin-top: 48px;
						padding-top: 32px;
						border-top: 1px solid rgba(255, 255, 255, 0.05);
						font-size: 14px;
						color: #4b5563;
						display: flex;
						flex-direction: column;
						gap: 8px;
					}
					.loader {
						width: 16px; height: 16px;
						border: 2px solid rgba(255, 255, 255, 0.1);
						border-top-color: var(--primary);
						border-radius: 50%;
						animation: spin 1s linear infinite;
						display: inline-block;
						vertical-align: middle;
						margin-right: 8px;
					}
					@keyframes spin { to { transform: rotate(360deg); } }
				</style>
			</head>
			<body>
				<div class="card">
					<div class="icon-container">
						<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round">
							<polyline points="20 6 9 17 4 12"></polyline>
						</svg>
					</div>
					<h1>Nexus Linked</h1>
					<p>Your YouTube connection is verified. Encryption keys have been synchronized securely with your local node.</p>
					
					<div class="footer">
						<span>You can safely close this window now.</span>
						<span style="font-size: 12px; opacity: 0.7;">Redirecting to Nexus App...</span>
					</div>
				</div>
				<script>
					setTimeout(() => {
						window.close();
					}, 3000);
				</script>
			</body>
			</html>
			`
			fmt.Fprint(w, html)
			go srv.Shutdown(context.Background())
		} else {
			fmt.Fprintf(w, "No code found.")
		}
	})

	srv.Handler = handler
	return srv.ListenAndServe()
}

func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

// extractGoogleSubFromToken extracts the Google subject ID from the OAuth token's ID token (JWT)
func extractGoogleSubFromToken(token *oauth2.Token) string {
	if token == nil {
		return ""
	}

	// The ID token is in token.Extra()["id_token"]
	idTokenRaw, ok := token.Extra("id_token").(string)
	if !ok || idTokenRaw == "" {
		return ""
	}

	// JWT format: header.payload.signature
	parts := strings.Split(idTokenRaw, ".")
	if len(parts) != 3 {
		return ""
	}

	// Decode the payload (second part)
	payload := parts[1]
	// Add padding if needed for base64 decoding
	padding := (4 - len(payload)%4) % 4
	payload += strings.Repeat("=", padding)

	decodedBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}

	// Parse JSON to extract 'sub' claim
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(decodedBytes, &claims); err != nil {
		return ""
	}

	return claims.Sub
}

func saveToken(path string, token *oauth2.Token) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
		var cleanEnv []string
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
				cleanEnv = append(cleanEnv, e)
			}
		}
		cmd.Env = cleanEnv
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		return
	}
	if err := cmd.Start(); err != nil {
		log.Printf("⚠️  Failed to open browser: %v", err)
	}
}
