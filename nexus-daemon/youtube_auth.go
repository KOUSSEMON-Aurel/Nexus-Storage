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

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/youtube/v3"
)

type YouTubeManager struct {
	config  *oauth2.Config
	service *youtube.Service
	mu      sync.RWMutex
	authed  bool
	user    string
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

	// Pro Scope: YoutubeScope allows Search and Deletion of ANY video (needed for cleanup)
	config, err := google.ConfigFromJSON(b, youtube.YoutubeScope)
	if err != nil {
		log.Printf("⚠️  YouTube: could not parse client_secret.json: %v", err)
		return m
	}
	config.RedirectURL = "http://localhost:8080"
	m.config = config
	m.TryLoadToken()

	// VALIDATION: If authenticated, check if we have the 'Search' scope by doing a tiny test call
	if m.authed {
		go func() {
			svc := m.GetService()
			if svc == nil { return }
			_, err := svc.Search.List([]string{"id"}).MaxResults(1).ForMine(true).Do()
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

	m.mu.Lock()
	m.service = service
	m.authed = true
	m.user = "koussemonaurel@gmail.com" // Mocking for now, could fetch from API
	m.mu.Unlock()
	return true
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
				<title>Nexus-Storage - Authenticated</title>
				<style>
					body { background-color: #0f172a; color: #f8fafc; font-family: 'Segoe UI', Tahoma, Geneva, Verdana, sans-serif; display: flex; justify-content: center; align-items: center; height: 100vh; margin: 0; }
					.card { background-color: #1e293b; padding: 40px; border-radius: 12px; box-shadow: 0 10px 25px rgba(0,0,0,0.5); text-align: center; max-width: 400px; border: 1px solid #334155; }
					.icon { background-color: #10b981; color: white; width: 60px; height: 60px; border-radius: 50%; display: flex; justify-content: center; align-items: center; font-size: 30px; margin: 0 auto 20px; box-shadow: 0 0 15px rgba(16, 185, 129, 0.5); }
					h1 { margin: 0 0 10px; font-size: 24px; }
					p { color: #94a3b8; font-size: 15px; line-height: 1.5; margin-bottom: 20px; }
					.btn { background-color: #3b82f6; color: white; border: none; padding: 10px 20px; border-radius: 6px; font-size: 14px; font-weight: 600; cursor: pointer; transition: background 0.2s; }
					.btn:hover { background-color: #2563eb; }
				</style>
			</head>
			<body>
				<div class="card">
					<div class="icon">✓</div>
					<h1>Authentication Successful</h1>
					<p>Your Google account has been securely linked to <b>Nexus-Storage</b>. You can now close this window and return to the application.</p>
					<button class="btn" onclick="window.close()">Close Window</button>
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
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	if err != nil {
		log.Printf("Failed to open browser: %v", err)
	}
}
