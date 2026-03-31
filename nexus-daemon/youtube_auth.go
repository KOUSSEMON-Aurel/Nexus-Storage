package main

import (
	"context"
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

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
	"io"
)

type YouTubeManager struct {
	config       *oauth2.Config
	service      *youtube.Service
	driveService *drive.Service
	mu           sync.RWMutex
	authed       bool
	user         string
	channelID    string
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

	// Pro Scope: YoutubeScope + Monitoring for real-time quota + Drive for manifest
	config, err := google.ConfigFromJSON(b, 
		youtube.YoutubeScope, 
		"https://www.googleapis.com/auth/monitoring.read",
		drive.DriveFileScope,
	)
	if err != nil {
		log.Printf("⚠️  YouTube: could not parse client_secret.json: %v", err)
		return m
	}
	config.RedirectURL = "http://localhost:8080"
	m.config = config
	m.TryLoadToken()

	// VALIDATION: If authenticated, check if we have the 'Search' scope by doing a minimal validation call
	if m.authed {
		go func() {
			svc := m.GetService()
			if svc == nil { return }
			// Use Videos.List (1 unit) instead of Search.List (100 units) for scope validation
			_, err := svc.Videos.List([]string{"id"}).MaxResults(1).Do()
			if err != nil && strings.Contains(err.Error(), "insufficientPermissions") {
				log.Printf("⚠️  OAuth Scope mismatch detected (Old Token). Forcing Re-Auth...")
				m.mu.Lock()
				m.authed = false
				m.mu.Unlock()
				// Delete old token file to prevent loop
				os.Remove(filepath.Join(getConfigDir(), "token.json"))
			}
		}()
	}
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

	m.mu.Lock()
	m.service = service
	m.driveService = driveService
	m.authed = true
	m.mu.Unlock()

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

func (m *YouTubeManager) FetchChannelID() {
	svc := m.GetService()
	if svc == nil { return }

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
	if len(b) == 0 { b, _ = os.ReadFile("client_secret.json") }
	var secret struct {
		Installed struct { ProjectID string `json:"project_id"` } `json:"installed"`
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
				Value struct { Int64Value string `json:"int64Value"` } `json:"value"`
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
			saveToken(filepath.Join(getConfigDir(), "token.json"), tok)
			m.TryLoadToken()

			html := `
			<!DOCTYPE html>
			<html>
			<head>
				<title>Nexus Storage - Authenticated</title>
				<meta name="viewport" content="width=device-width, initial-scale=1.0">
				<style>
					:root { --primary: #1A73E8; --bg: #0F172A; }
					body { 
						background: radial-gradient(circle at top right, #1E293B, #0F172A);
						color: #F8FAFC; 
						font-family: 'Inter', -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
						display: flex; justify-content: center; align-items: center; 
						height: 100vh; margin: 0; overflow: hidden;
					}
					.glass {
						background: rgba(30, 41, 59, 0.7);
						backdrop-filter: blur(12px);
						-webkit-backdrop-filter: blur(12px);
						padding: 48px;
						border-radius: 32px;
						border: 1px solid rgba(255, 255, 255, 0.1);
						box-shadow: 0 25px 50px -12px rgba(0, 0, 0, 0.5);
						text-align: center;
						max-width: 420px;
						animation: slideUp 0.6s cubic-bezier(0.16, 1, 0.3, 1);
					}
					@keyframes slideUp {
						from { opacity: 0; transform: translateY(20px) scale(0.98); }
						to { opacity: 1; transform: translateY(0) scale(1); }
					}
					.icon-circle {
						width: 80px; height: 80px;
						background: linear-gradient(135deg, #34D399, #10B981);
						border-radius: 24px;
						display: flex; justify-content: center; align-items: center;
						margin: 0 auto 32px;
						box-shadow: 0 20px 40px rgba(16, 185, 129, 0.3);
						transform: rotate(-5deg);
					}
					.check { font-size: 40px; color: white; filter: drop-shadow(0 2px 4px rgba(0,0,0,0.2)); }
					h1 { margin: 0 0 12px; font-size: 28px; font-weight: 700; letter-spacing: -0.5px; }
					p { color: #94A3B8; font-size: 16px; line-height: 1.6; margin-bottom: 32px; font-weight: 400; }
					.btn { 
						background: var(--primary);
						color: white; border: none; 
						padding: 16px 32px; border-radius: 16px;
						font-size: 15px; font-weight: 600; cursor: pointer;
						transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
						box-shadow: 0 8px 16px rgba(26, 115, 232, 0.2);
						width: 100%;
					}
					.btn:hover { background: #2563EB; transform: translateY(-2px); box-shadow: 0 12px 20px rgba(26, 115, 232, 0.3); }
					.btn:active { transform: translateY(0); }
				</style>
			</head>
			<body>
				<div class="glass">
					<div class="icon-circle">
						<span class="check">✓</span>
					</div>
					<h1>Nexus Linked</h1>
					<p>Your YouTube connection is verified. Encryption keys have been synchronized securely.</p>
					<button class="btn" onclick="window.close()">Back to Nexus</button>
				</div>
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
