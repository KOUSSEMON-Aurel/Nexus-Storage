package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dbPath := flag.String("db", "nexus.db", "Path to the SQLite database")
	flag.Parse()

	fmt.Println("🚀 NexusStorage Daemon starting (WebDAV Mode)...")

	// 1. Initialize DB
	db := &Database{}
	if err := db.Init(*dbPath); err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 2. Initialize Core
	core := &NexusCore{}

	// 3. Initialize YouTube OAuth Manager
	// Non-blocking authentication
	ytManager, err := NewYouTubeManager()
	if err != nil {
		log.Printf("Warning: YouTube authentication check failed: %v", err)
	}

	// 4. Initialize Task Queue
	queue := TaskQueue{}
	queue.Init(core, db, ytManager)

	// 5. Start API & WebDAV Server for GUI
	api := &APIServer{db: db, queue: &queue, ytManager: ytManager}
	api.Start(8081)

	// Keep alive until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	fmt.Println("✅ Daemon is running. WebDAV accessible at http://localhost:8081/webdav/")
	<-sigChan
	
	fmt.Println("\n👋 Shutting down NexusStorage...")
}
