package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	queue.Init(core, db, ytManager)

	// 5. Manifest Backup is now triggered only after successful uploads (more efficient)
	// (hooks removed from here to avoid over-frequent cloud sync)


	// 6. Start API & WebDAV Server for GUI
	api := &APIServer{db: db, queue: &queue, ytManager: ytManager}
	
	// Auto-mount on Linux after a short delay
	time.AfterFunc(3*time.Second, func() {
		autoMountLinux()
	})

	api.Start(8081)

	// Keep alive until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	fmt.Println("✅ Daemon is running. WebDAV accessible at http://localhost:8081/webdav/")
	<-sigChan
	
	fmt.Println("\n👋 Shutting down NexusStorage...")
}

func autoMountLinux() {
	url := "dav://127.0.0.1:8081/webdav/"
	log.Printf("🛠️ Attempting to auto-mount virtual disk: %s", url)
	
	// Use gio mount (standard on GNOME/KDE)
	exec.Command("gio", "mount", url).Run()
	
	// We don't fatal on error because might already be mounted or non-GIO system
	log.Println("✅ Virtual disk mount request sent to system.")
}
