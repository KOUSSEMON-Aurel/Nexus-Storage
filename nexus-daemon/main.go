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

// getHostTriple returns the Tauri-compatible host triple (e.g., x86_64-unknown-linux-gnu)
func getHostTriple() string {
	// Simple mapping for common platforms
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x86_64"
	} else if arch == "arm64" {
		arch = "aarch64"
	}

	os := runtime.GOOS
	if os == "linux" {
		return fmt.Sprintf("%s-unknown-linux-gnu", arch)
	} else if os == "darwin" {
		return fmt.Sprintf("%s-apple-darwin", arch)
	} else if os == "windows" {
		return fmt.Sprintf("%s-pc-windows-msvc", arch)
	}
	return fmt.Sprintf("%s-unknown-%s", arch, os)
}

// has checks if a command exists in the system PATH
func has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func main() {
	configDir := getConfigDir()
	dbPath := flag.String("db", filepath.Join(configDir, "nexus.db"), "Path to the SQLite database")
	flag.Parse()

	fmt.Println("🚀 NexusStorage Daemon starting (FUSE Engine)...")

	// 0. Clean up orphaned temp folders older than 1 hour to prevent disk leak
	tmpFiles, _ := filepath.Glob(filepath.Join(os.TempDir(), "nexus-*"))
	for _, f := range tmpFiles {
		if info, err := os.Stat(f); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(f)
		}
	}

	// 0. Verify ffmpeg (warning only when running from bundled sidecar)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		log.Printf("⚠️  WARNING: 'ffmpeg' not found in PATH. Upload/download may fail if not bundled.")
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

	// 5. Initialize Cache Manager (LRU disk cache for shards)
	cacheMaxBytes := int64(0) // 0 = use default (10 GB)
	if v, ok := db.GetKV("cache_max_bytes"); ok {
		fmt.Sscanf(v, "%d", &cacheMaxBytes)
	}
	cacheMgr, err := NewCacheManager(db, cacheMaxBytes)
	if err != nil {
		log.Printf("⚠️  Cache manager init failed (continuing without cache): %v", err)
		cacheMgr = nil
	} else {
		log.Printf("✅ Disk cache initialized at %s", cacheMgr.CacheDir)
	}

	// 6. Initialize Playlist Manager (V2 Cloud Structure)
	pm := NewPlaylistManager(ytManager, db, core)
	queue.Init(core, db, ytManager, pm, cacheMgr)

	// 6b. Auto-sync cloud manifest 10s after auth
	go func() {
		time.Sleep(5 * time.Second)
		if ytManager.IsAuthenticated() {
			if err := pm.EnsureBasePlaylists(); err != nil {
				log.Printf("⚠️  Playlist Manager: %v", err)
			}
			time.Sleep(5 * time.Second) // extra delay so playlists are ready
			// 6c. Auto-purge trash (default 30 days)
			purgeDays := 30
			if v, ok := db.GetKV("trash_purge_days"); ok {
				fmt.Sscanf(v, "%d", &purgeDays)
			}
			if purgeDays > 0 {
				log.Printf("🧹 Sweeping trash (Auto-purge older than %d days)...", purgeDays)
				deletedVids, err := db.CleanupTrash(purgeDays)
				if err == nil && len(deletedVids) > 0 {
					log.Printf("🗑️  Purging %d expired cloud shards...", len(deletedVids))
					for _, vid := range deletedVids {
						queue.AddTask(&Task{
							ID:        vid,
							Type:      TaskDelete,
							Status:    "Pending Purge",
							CreatedAt: time.Now(),
						})
					}
				}
			}
			log.Printf("✅ Auto-sync on startup completed.")
		}
	}()

	// 7. Start API & Internal VFS Server for GUI
	api := &APIServer{db: db, queue: &queue, ytManager: ytManager, pm: pm, cache: cacheMgr}

	go api.Start(8081)

	// Keep alive until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("✅ Daemon is running. FUSE Bridge active at :8081")
	<-sigChan

	fmt.Println("\n👋 Shutting down NexusStorage...")
	
	// Final Sync before exit
	log.Println("🔄 Performing final manifest backup...")
	queue.QueueManifestBackup()
	
	// Wait a bit for the backup task to start/finish if it's the only one
	time.Sleep(2 * time.Second)
	
	unmountVirtualDisk()
}

// ─── Universal Smart Mounting & Unmounting ────────────────────────────────────

func unmountVirtualDisk() {
	mountPath := filepath.Join(os.Getenv("HOME"), "Nexus-Storage")
	if runtime.GOOS == "linux" {
		exec.Command("fusermount", "-u", mountPath).Run()
	} else if runtime.GOOS == "darwin" {
		exec.Command("umount", mountPath).Run()
	} else if runtime.GOOS == "windows" {
		exec.Command("taskkill", "/IM", "rclone.exe", "/F").Run()
	}
	log.Printf("🔌 [SmartMount] Unmount requested.")
}

func isVirtualDiskMounted() bool {
	mountPath := filepath.Join(os.Getenv("HOME"), "Nexus-Storage")
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		out, err := exec.Command("mount").Output()
		if err == nil {
			return strings.Contains(string(out), mountPath)
		}
	} else if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist").Output()
		if err == nil {
			return strings.Contains(string(out), "rclone.exe")
		}
	}
	return false
}

// autoMountVirtualDisk is the universal cross-platform smart mounter.
// It probes available commands (more reliable than $XDG_CURRENT_DESKTOP)
// and chains through a sensible fallback stack.
func autoMountVirtualDisk() {
	mountPath := filepath.Join(os.Getenv("HOME"), "Nexus-Storage")
	const (
		httpURL   = "http://127.0.0.1:8081/vfs/"
		davURL    = "dav://127.0.0.1:8081/vfs/"
		vfsURL = "vfs://127.0.0.1:8081/vfs/"
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
		log.Printf("🛠️  [SmartMount] Linux detected — probing FUSE provider...")

		// Use system rclone (Standard Linux way)
		if has("rclone") {
			log.Printf("🚀 [SmartMount] Attempting Rclone FUSE mount at %s", mountPath)
			os.MkdirAll(mountPath, 0755)
			
			// Unmount first if already mounted (clean start)
			exec.Command("fusermount", "-u", mountPath).Run()

			// rclone mount :webdav: /path --webdav-url http://...
			args := []string{
				"mount", ":webdav:", mountPath,
				"--webdav-url", httpURL,
				"--vfs-cache-mode", "full",
				"--vfs-cache-max-age", "24h",
				"--vfs-cache-max-size", "10G",
				"--vfs-read-chunk-size", "128M",
				"--daemon", // Run in background
				"--volname", "Nexus Storage",
			}
			
			cmd := exec.Command("rclone", args...)
			cmd.Env = cleanEnv
			if err := cmd.Run(); err == nil {
				log.Printf("✅ [SmartMount] Rclone FUSE mounted successfully.")
				// Open file manager in the mount point
				for _, fm := range []string{"dolphin", "nautilus", "thunar", "xdg-open"} {
					if has(fm) && run(fm, mountPath) {
						break
					}
				}
				return
			} else {
				log.Printf("❌ [SmartMount] Rclone FUSE failed: %v", err)
			}
		} else {
			log.Printf("❌ [SmartMount] System 'rclone' missing. Please install it (e.g. sudo apt install rclone).")
		}

	// ─── Windows ─────────────────────────────────────────────────────────────
	case "windows":
		log.Printf("🪟 [SmartMount] Windows detected — probing FUSE provider...")
		
		driveLetter := "N:" 
		if has("rclone") {
			log.Printf("🚀 [SmartMount] Attempting Rclone FUSE mount at %s", driveLetter)
			exec.Command("taskkill", "/IM", "rclone*", "/F").Run()

			args := []string{
				"mount", ":webdav:", driveLetter,
				"--webdav-url", httpURL,
				"--vfs-cache-mode", "full",
				"--no-console",
				"--volname", "Nexus Storage",
			}
			
			cmd := exec.Command("rclone", args...)
			cmd.Env = cleanEnv
			if err := cmd.Start(); err == nil {
				log.Printf("✅ [SmartMount] Rclone FUSE mount dispatched.")
				time.Sleep(2 * time.Second)
				exec.Command("explorer", driveLetter).Start()
				return
			}
		}
		log.Printf("❌ [SmartMount] System 'rclone' missing or WinFsp not installed.")

	// ─── macOS ───────────────────────────────────────────────────────────────
	case "darwin":
		log.Printf("🍎 [SmartMount] macOS detected — probing FUSE provider...")
		
		if has("rclone") {
			log.Printf("🚀 [SmartMount] Attempting Rclone FUSE mount at %s", mountPath)
			os.MkdirAll(mountPath, 0755)
			
			args := []string{
				"mount", ":webdav:", mountPath,
				"--webdav-url", httpURL,
				"--vfs-cache-mode", "full",
				"--daemon",
				"--volname", "Nexus Storage",
			}
			
			cmd := exec.Command("rclone", args...)
			cmd.Env = cleanEnv
			if err := cmd.Run(); err == nil {
				log.Printf("✅ [SmartMount] Rclone FUSE mounted successfully.")
				run("open", mountPath)
				return
			}
		}
		log.Printf("❌ [SmartMount] System 'rclone' missing or MacFUSE not installed.")
	}

	log.Printf("✅ [SmartMount] Virtual disk mount dispatched.")
}
