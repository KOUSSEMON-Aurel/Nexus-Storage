package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	mountTarget := flag.String("mount", "/tmp/nexus-drive", "Path to mount the virtual drive")
	dbPath := flag.String("db", "nexus.db", "Path to the SQLite database")
	flag.Parse()

	fmt.Println("🚀 NexusStorage Daemon starting...")

	// 1. Initialize DB
	db := &Database{}
	if err := db.Init(*dbPath); err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 2. Initialize Core
	core := &NexusCore{}

	// 3. Initialize Task Queue
	queue := TaskQueue{}
	queue.Init(core, db)

	// 4. Setup Mount Point
	absMountPath, _ := filepath.Abs(*mountTarget)
	if _, err := os.Stat(absMountPath); os.IsNotExist(err) {
		err := os.MkdirAll(absMountPath, 0755)
		if err != nil {
			log.Fatalf("Could not create mount directory: %v", err)
		}
	}

	// 5. Start FUSE (Placeholder logic for now)
	fmt.Printf("📂 Mounting virtual drive at: %s\n", absMountPath)
	// go StartFuse(absMountPath, queue)

	// Keep alive until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	fmt.Println("✅ Daemon is running. Press Ctrl+C to stop.")
	<-sigChan
	
	fmt.Println("\n👋 Shutting down NexusStorage...")
}
