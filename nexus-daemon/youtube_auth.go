package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
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

func NewYouTubeManager() (*YouTubeManager, error) {
	b, err := os.ReadFile("client_secret.json")
	if err != nil {
		return nil, fmt.Errorf("unable to read client secret file: %v", err)
	}

	config, err := google.ConfigFromJSON(b, youtube.YoutubeUploadScope)
	if err != nil {
		return nil, fmt.Errorf("unable to parse client secret file to config: %v", err)
	}
	config.RedirectURL = "http://localhost:8080"

	m := &YouTubeManager{config: config}
	m.TryLoadToken()
	return m, nil
}

func (m *YouTubeManager) TryLoadToken() bool {
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
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
			saveToken("token.json", tok)
			m.TryLoadToken()

			fmt.Fprintf(w, "Authentication successful! You can close this window.")
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
