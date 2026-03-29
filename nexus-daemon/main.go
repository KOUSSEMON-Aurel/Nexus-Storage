package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func main() {
	configDir := getConfigDir()
	dbPath := flag.String("db", filepath.Join(configDir, "nexus.db"), "Path to the SQLite database")
	flag.Parse()

	fmt.Println("🚀 NexusStorage Daemon starting (WebDAV Mode)...")

	// 0. Clean up orphaned temp folders older than 1 hour to prevent disk leak
	tmpFiles, _ := filepath.Glob(filepath.Join(os.TempDir(), "nexus-*"))
	for _, f := range tmpFiles {
		if info, err := os.Stat(f); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(f)
		}
	}

	// 0. Verify crucial dependencies
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Fatal("❌ FATAL ERROR: 'ffmpeg' is not installed or not in PATH. It is required for encrypting data to videos.\n💡 Please install it (e.g., 'sudo pacman -S ffmpeg' or 'sudo apt install ffmpeg').")
	}

	// 1. Initialize DB
	db := &Database{}
	if err := db.Init(*dbPath); err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 2. Initialize Core
	core := &NexusCore{}

	// 3. Initialize YouTube OAuth Manager
	// Non-blocking authentication — never nil, always returns a manager
	ytManager := NewYouTubeManager()

	// 4. Initialize Task Queue
	queue := TaskQueue{}

	// 5. Initialize Playlist Manager (V2 Cloud Structure)
	pm := NewPlaylistManager(ytManager, db)
	queue.Init(core, db, ytManager, pm)

	go func() {
		// Wait for auth to be ready
		time.Sleep(5 * time.Second)
		if ytManager.IsAuthenticated() {
			if err := pm.EnsureBasePlaylists(); err != nil {
				log.Printf("⚠️  Playlist Manager: %v", err)
			}
		}
	}()

	// 5. Manifest Backup is now triggered only after successful uploads (more efficient)
	// (hooks removed from here to avoid over-frequent cloud sync)

	// 6. Start API & WebDAV Server for GUI
	api := &APIServer{db: db, queue: &queue, ytManager: ytManager}

	api.Start(8081)

	// Keep alive until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("✅ Daemon is running. WebDAV accessible at http://localhost:8081/webdav/")
	<-sigChan

	fmt.Println("\n👋 Shutting down NexusStorage...")
}

// autoMountVirtualDisk is the universal cross-platform smart mounter.
// It probes available commands (more reliable than $XDG_CURRENT_DESKTOP)
// and chains through a sensible fallback stack.
func autoMountVirtualDisk() {
	const (
		httpURL   = "http://127.0.0.1:8081/webdav/"
		davURL    = "dav://127.0.0.1:8081/webdav/"
		webdavURL = "webdav://127.0.0.1:8081/webdav/"
		mountDir  = "/mnt/nexus"
	)

	// Clean env — Tauri injects LD_LIBRARY_PATH which breaks cold browser/file-manager launches
	cleanEnv := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			cleanEnv = append(cleanEnv, e)
		}
	}

	run := func(name string, args ...string) bool {
		cmd := exec.Command(name, args...)
		cmd.Env = cleanEnv
		return cmd.Start() == nil
	}

	has := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}

	switch runtime.GOOS {

	// ─── Linux ───────────────────────────────────────────────────────────────
	case "linux":
		log.Printf("🛠️  [SmartMount] Linux detected — probing tools...")

		// 1. KDE / KIO (Dolphin) — native WebDAV, no gio needed
		if has("dolphin") {
			log.Printf("🐬 [SmartMount] dolphin %s", webdavURL)
			if run("dolphin", webdavURL) {
				return
			}
		}

		// 2. GNOME / gio — proper GVFS DAV bookmark (appears in sidebar)
		if has("gio") {
			log.Printf("🎨 [SmartMount] gio mount %s", davURL)
			_ = exec.Command("gio", "mount", davURL).Run()
			if has("xdg-open") {
				run("xdg-open", davURL)
			}
			return
		}

		// 3. Universal xdg-open via http:// → Thunar, Nemo, Caja, PCManFM, etc.
		if has("xdg-open") {
			log.Printf("📂 [SmartMount] xdg-open %s", httpURL)
			if run("xdg-open", httpURL) {
				return
			}
		}

		// 4. davfs2 CLI — headless / i3 / Openbox
		if has("mount.davfs") {
			log.Printf("🔧 [SmartMount] davfs2 → %s", mountDir)
			_ = os.MkdirAll(mountDir, 0755)
			_ = exec.Command("mount", "-t", "davfs", httpURL, mountDir).Run()
			for _, fm := range []string{"xdg-open", "thunar", "pcmanfm", "caja", "nemo"} {
				if has(fm) {
					run(fm, mountDir)
					break
				}
			}
			return
		}

		// 5. Ultimate fallback — open in browser
		for _, b := range []string{"xdg-open", "firefox", "chromium", "google-chrome"} {
			if has(b) {
				log.Printf("🌐 [SmartMount] browser fallback %s", httpURL)
				run(b, httpURL)
				return
			}
		}

	// ─── Windows ─────────────────────────────────────────────────────────────
	case "windows":
		log.Printf("🪟 [SmartMount] Windows → net use Z: %s", httpURL)
		_ = exec.Command("net", "use", "Z:", httpURL, "/persistent:yes").Run()
		exec.Command("explorer", "Z:").Start()

	// ─── macOS ───────────────────────────────────────────────────────────────
	case "darwin":
		log.Printf("🍎 [SmartMount] macOS → open %s", httpURL)
		run("open", httpURL)
	}

	log.Printf("✅ [SmartMount] Virtual disk mount dispatched.")
}
