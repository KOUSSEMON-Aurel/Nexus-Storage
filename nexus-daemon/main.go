package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
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

// findBinary attempts to find a binary in the PATH or next to the current executable
func findBinary(name string) string {
	// 1. Try system PATH
	if p, err := exec.LookPath(name); err == nil {
		return p
	}

	// 2. Try various paths relative to the current executable
	if execPath, err := os.Executable(); err == nil {
		dir := filepath.Dir(execPath)
		triple := getHostTriple()
		
		// Names to try: "rclone", "rclone.exe", "rclone-x86_64-pc-windows-msvc.exe"
		candidates := []string{
			name,
			name + "-" + triple,
		}

		searchDirs := []string{dir, filepath.Join(dir, "bin"), filepath.Join(dir, "resources")}

		for _, sDir := range searchDirs {
			for _, c := range candidates {
				target := filepath.Join(sDir, c)
				if runtime.GOOS == "windows" && !strings.HasSuffix(target, ".exe") {
					// try both .exe and naked
					if _, err := os.Stat(target + ".exe"); err == nil {
						return target + ".exe"
					}
				}
				if _, err := os.Stat(target); err == nil {
					return target
				}
			}
		}
	}

	return ""
}

func has(name string) bool {
	return findBinary(name) != ""
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

	// 0. Verify port 8081 is free
	if ln, err := net.Listen("tcp", ":8081"); err != nil {
		log.Fatalf("❌ Port 8081 is already in use. Please close any existing NexusStorage processes: %v", err)
	} else {
		ln.Close()
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

	// 6a. Initialize Sync Manager
	syncMgr := NewSyncManager(db, ytManager, pm, *dbPath)
	queue.SetSyncManager(syncMgr)

	// 6b. Auto-sync cloud manifest 10s after auth
	go func() {
		time.Sleep(5 * time.Second)
		if ytManager.IsAuthenticated() {
			if err := pm.EnsureBasePlaylists(); err != nil {
				log.Printf("⚠️  Playlist Manager: %v", err)
			}
			time.Sleep(5 * time.Second) // extra delay so playlists are ready
			
			// FULL STARTUP MATRIX
			log.Printf("🔍 Running startup DB state matrix...")
			
			// 1. Check for WAL and Checkpoint if needed
			walPath := (*dbPath) + "-wal"
			if info, err := os.Stat(walPath); err == nil && info.Size() > 0 {
				log.Printf("🧹 Found non-empty WAL, checkpointing...")
				db.Checkpoint()
			}

			// 2. Integrity Check
			integrityErr := db.IntegrityCheck()
			localLSN, _ := db.GetLocalLSN()
			remoteManifest, _ := syncMgr.GetRemoteManifest()

			if integrityErr != nil {
				log.Printf("❌ DB CORRUPTION DETECTED: %v", integrityErr)
				// Quarantaine
				corruptPath := (*dbPath) + ".corrupt"
				os.Rename(*dbPath, corruptPath)
				log.Printf("📁 Corrupted DB moved to %s", corruptPath)

				if remoteManifest != nil {
					log.Printf("📥 Remote backup found, attempting recovery pull...")
					if err := syncMgr.PullDBFromDrive(true); err != nil {
						log.Printf("❌ Recovery pull failed: %v", err)
					}
				} else {
					log.Printf("⚠️ No remote backup found. Starting with fresh DB.")
					db.Init(*dbPath)
				}
			} else if localLSN == 0 {
				log.Printf("📥 Local DB is empty, checking for cloud backup...")
				if remoteManifest != nil {
					if err := syncMgr.PullDBFromDrive(false); err != nil {
						log.Printf("ℹ️ Initial pull skipped: %v", err)
					}
				}
			} else {
				log.Printf("✅ Local DB is healthy (LSN %d)", localLSN)
				if remoteManifest != nil && remoteManifest.LSN > localLSN {
					log.Printf("📥 Remote DB is newer (Remote: %d), Pulling...", remoteManifest.LSN)
					syncMgr.PullDBFromDrive(false)
				}
			}

			log.Printf("✅ Auto-sync on startup completed.")
		}
	}()

	// 7. Start API & Internal VFS Server for GUI
	api := &APIServer{db: db, queue: &queue, ytManager: ytManager, pm: pm, cache: cacheMgr, syncMgr: syncMgr, dbPath: *dbPath}

	go api.Start(8081)

	// 8. OPTIMIZATION #4: Daily trash cleanup scheduler
	// Instead of cleaning trash on every startup (expensive operation),
	// schedule it to run once per day at 3:00 AM
	go scheduleTrashCleanup(db, &queue)

	// 9. ROBUSTNESS #2: Orphaned video cleanup (handles race conditions in delete operations)
	// Run at startup and then every hour to catch any orphaned videos
	go scheduleOrphanCleanup(&queue)

	// Keep alive until interrupted
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("✅ Daemon is running. FUSE Bridge active at :8081")
	<-sigChan

	fmt.Println("\n👋 Shutting down NexusStorage...")
	
	// Final Sync before exit: Use strict Push logic
	log.Println("🔄 Performing strict final DB backup to Drive...")
	if err := syncMgr.PushDBToDrive(); err != nil {
		log.Printf("⚠️  Final backup failed: %v", err)
	} else {
		log.Printf("✅ Final backup completed.")
	}
	
	unmountVirtualDisk()
}

// scheduleTrashCleanup runs daily trash cleanup at 3:00 AM instead of on every startup
// OPTIMIZATION #4: Prevents quota waste from repeated cleanup operations
// Only executes once per day, avoiding redundant YouTube API calls
func scheduleTrashCleanup(db *Database, queue *TaskQueue) {
	for {
		// Calculate next cleanup time (3:00 AM today or tomorrow)
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
		
		// If 3 AM has already passed today, schedule for tomorrow
		if now.After(next) {
			next = next.AddDate(0, 0, 1)
		}
		
		waitDuration := next.Sub(now)
		log.Printf("⏰ Trash cleanup scheduled for %s (in %v)", next.Format("15:04:05"), waitDuration.Round(time.Second))
		
		// Sleep until the scheduled cleanup time
		time.Sleep(waitDuration)
		
		// Execute cleanup
		log.Printf("🧹 [SCHEDULED] Running daily trash cleanup...")
		purgeDays := 30
		if v, ok := db.GetKV("trash_purge_days"); ok {
			fmt.Sscanf(v, "%d", &purgeDays)
		}
		
		if purgeDays > 0 {
			if deletedVids, err := db.CleanupTrash(purgeDays); err == nil && len(deletedVids) > 0 {
				log.Printf("🗑️  [SCHEDULED] Queueing %d expired cloud shards for deletion...", len(deletedVids))
				for _, vid := range deletedVids {
					queue.AddTask(&Task{
						ID:        vid,
						Type:      TaskDelete,
						Status:    "Pending Purge (Scheduled)",
						CreatedAt: time.Now(),
					})
				}
				log.Printf("✅ [SCHEDULED] Trash cleanup completed at %s", time.Now().Format("15:04:05"))
			} else if err != nil {
				log.Printf("❌ [SCHEDULED] Trash cleanup failed: %v", err)
			} else {
				log.Printf("ℹ️  [SCHEDULED] No expired trash found")
			}
		}
	}
}

// scheduleOrphanCleanup runs cleanup for orphaned videos on startup and hourly thereafter
func scheduleOrphanCleanup(queue *TaskQueue) {
	// Run at startup immediately
	log.Printf("🔧 [STARTUP] Running orphan video cleanup...")
	if err := queue.CleanupOrphanedVideos(); err != nil {
		log.Printf("❌ [STARTUP] Orphan cleanup failed: %v", err)
	} else {
		log.Printf("✅ [STARTUP] Orphan cleanup completed")
	}

	// Then run every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		log.Printf("🔧 [HOURLY] Running orphan video cleanup...")
		if err := queue.CleanupOrphanedVideos(); err != nil {
			log.Printf("❌ [HOURLY] Orphan cleanup failed: %v", err)
		} else {
			log.Printf("✅ [HOURLY] Orphan cleanup completed")
		}
	}
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
			rclonePath := findBinary("rclone")
			if rclonePath == "" {
				log.Printf("❌ [SmartMount] Rclone binary not found.")
				return
			}

			if !checkWinFsp() {
				log.Printf("⚠️  [SmartMount] WinFsp missing. Attempting automatic installation...")
				if err := installWinFsp(); err != nil {
					log.Printf("❌ [SmartMount] Auto-install failed: %v", err)
					log.Printf("💡 [SmartMount] Please install WinFsp manually: https://winfsp.dev/")
					return
				}
				log.Printf("✅ [SmartMount] WinFsp installer launched. Please complete the setup and restart the app.")
				return
			}

			log.Printf("🚀 [SmartMount] Attempting Rclone FUSE mount at %s", driveLetter)
			exec.Command("taskkill", "/IM", "rclone*", "/F").Run()

			args := []string{
				"mount", ":webdav:", driveLetter,
				"--webdav-url", httpURL,
				"--vfs-cache-mode", "full",
				"--network-mode",
				"--no-console",
				"--volname", "Nexus Storage",
			}
			
			cmd := exec.Command(rclonePath, args...)
			cmd.Env = cleanEnv
			if err := cmd.Start(); err == nil {
				log.Printf("✅ [SmartMount] Rclone FUSE mount dispatched.")
				time.Sleep(2 * time.Second)
				exec.Command("explorer", driveLetter).Start()
				return
			}
		log.Printf("❌ [SmartMount] Rclone mount failed or WinFsp missing.")

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

func checkWinFsp() bool {
	if runtime.GOOS != "windows" {
		return true
	}
	// Check for WinFsp installation in Program Files
	paths := []string{
		os.Getenv("ProgramFiles") + "\\WinFsp\\bin\\launchctl-x64.exe",
		os.Getenv("ProgramFiles(x86)") + "\\WinFsp\\bin\\launchctl-x86.exe",
		"C:\\Program Files\\WinFsp\\bin\\launchctl-x64.exe", // Hardcoded fallback
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// installWinFsp downloads and launches the WinFsp MSI
func installWinFsp() error {
	msiURL := "https://github.com/winfsp/winfsp/releases/download/v2.1/winfsp-2.1.25156.msi"
	msiPath := filepath.Join(os.TempDir(), "winfsp-install.msi")

	log.Printf("📥 Downloading WinFsp installer...")
	
	// Use powershell to download (standard on all Windows)
	dlCmd := fmt.Sprintf("Invoke-WebRequest -Uri '%s' -OutFile '%s'", msiURL, msiPath)
	if err := exec.Command("powershell", "-Command", dlCmd).Run(); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	log.Printf("🚀 Launching WinFsp MSI...")
	// Launch MSI silently or with UI (UI is better to avoid hidden failures)
	return exec.Command("msiexec", "/i", msiPath).Start()
}
