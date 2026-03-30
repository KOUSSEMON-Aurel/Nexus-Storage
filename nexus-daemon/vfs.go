package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"
)

// NexusFS implements webdav.FileSystem
type NexusFS struct {
	db           *Database
	queue        *TaskQueue
	cache        string
	pendingMu    sync.RWMutex
	pendingFiles map[string]int64 // cleanName -> size (set at write-open time)
}

func NewNexusFS(db *Database, queue *TaskQueue) *NexusFS {
	home, _ := os.UserHomeDir()
	cache := filepath.Join(home, ".nexus", "cache")
	os.MkdirAll(cache, 0755)
	return &NexusFS{db: db, queue: queue, cache: cache, pendingFiles: make(map[string]int64)}
}

// markPending registers a file as being written (so Stat returns OK immediately)
func (fs *NexusFS) markPending(name string, size int64) {
	fs.pendingMu.Lock()
	fs.pendingFiles[name] = size
	fs.pendingMu.Unlock()
}

// clearPending removes a file from the pending registry once its upload is done
func (fs *NexusFS) clearPending(name string) {
	fs.pendingMu.Lock()
	delete(fs.pendingFiles, name)
	fs.pendingMu.Unlock()
}

// isPending checks if a file is currently being written
func (fs *NexusFS) isPending(name string) (int64, bool) {
	fs.pendingMu.RLock()
	defer fs.pendingMu.RUnlock()
	size, ok := fs.pendingFiles[name]
	return size, ok
}

func (fs *NexusFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	cleanName := strings.TrimPrefix(name, "/")
	parts := strings.Split(cleanName, "/")
	
	var currentParent *int64
	for i, part := range parts {
		if part == "" { continue }
		id, err := fs.db.CreateFolder(part, currentParent)
		if err != nil {
			return err
		}
		if i < len(parts)-1 {
			currentParent = &id
		}
	}
	return nil
}

func (fs *NexusFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	cleanName := strings.TrimPrefix(name, "/")
	if cleanName == "" {
		return &NexusDir{fs: fs, folderID: nil}, nil
	}

	// Resolve the path to get the parent and name
	parts := strings.Split(cleanName, "/")
	var currentParent *int64
	for i, part := range parts {
		if part == "" { continue }
		
		// If last part, check if it's a file or folder
		if i == len(parts)-1 {
			// Check for folder first
			subfolders, _ := fs.db.ListSubfolders(currentParent)
			for _, sf := range subfolders {
				if sf.Name == part {
					return &NexusDir{fs: fs, folderID: &sf.ID}, nil
				}
			}
			
			// Check for file
			files, _ := fs.db.ListFilesByFolder(currentParent)
			var record *FileRecord
			for _, f := range files {
				if filepath.Base(f.Path) == part {
					record = &f
					break
				}
			}
			
			if record != nil || (flag&os.O_CREATE != 0) {
				return fs.openFileInternal(cleanName, record, flag, perm, currentParent)
			}
			return nil, os.ErrNotExist
		}
		
		// Intermediate part MUST be a folder
		subfolders, _ := fs.db.ListSubfolders(currentParent)
		found := false
		for _, sf := range subfolders {
			if sf.Name == part {
				currentParent = &sf.ID
				found = true
				break
			}
		}
		if !found {
			return nil, os.ErrNotExist
		}
	}
	
	return nil, os.ErrNotExist
}

func (fs *NexusFS) openFileInternal(cleanName string, _ *FileRecord, flag int, perm os.FileMode, parentID *int64) (webdav.File, error) {
	fullPath := filepath.Join(fs.cache, cleanName)
	
	// Create cache dir if it doesn't exist
	os.MkdirAll(filepath.Dir(fullPath), 0755)

	// If opening for writing: register as pending immediately so Stat works
	if flag&os.O_WRONLY != 0 || flag&os.O_RDWR != 0 || flag&os.O_CREATE != 0 {
		fs.markPending(cleanName, 0)
		f, err := os.OpenFile(fullPath, flag, perm)
		if err != nil {
			fs.clearPending(cleanName)
			return nil, err
		}
		return &NexusFile{File: f, fs: fs, name: cleanName, isWrite: true, parentID: parentID}, nil
	}

	// Opening for reading (already handled in OpenFile for non-existent)
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	return &NexusFile{File: f, fs: fs, name: cleanName, isWrite: false, parentID: parentID}, nil
}

func (fs *NexusFS) RemoveAll(ctx context.Context, name string) error {
	cleanName := strings.TrimPrefix(name, "/")
	files, _ := fs.db.ListFiles()
	for _, f := range files {
		if filepath.Base(f.Path) == cleanName {
			fs.db.SoftDelete(f.ID)
			break
		}
	}
	return os.RemoveAll(filepath.Join(fs.cache, cleanName))
}

func (fs *NexusFS) Rename(ctx context.Context, oldName, newName string) error {
	return os.Rename(filepath.Join(fs.cache, oldName), filepath.Join(fs.cache, newName))
}

func (fs *NexusFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	cleanName := strings.TrimPrefix(name, "/")
	if cleanName == "" {
		return os.Stat(fs.cache)
	}
	// 1. Prefer real cache stat
	fi, err := os.Stat(filepath.Join(fs.cache, cleanName))
	if err == nil {
		return fi, nil
	}

	// 2. Check pending writes: file is being uploaded, report it as existing
	if size, ok := fs.isPending(cleanName); ok {
		base := filepath.Base(cleanName)
		return &FakeFileInfo{name: base, size: size, modTime: time.Now(), isDir: false}, nil
	}

	// Resolve hierarchy to find record
	parts := strings.Split(cleanName, "/")
	var currentParent *int64
	for i, part := range parts {
		if part == "" { continue }
		if i == len(parts)-1 {
			// Check folders
			subfolders, _ := fs.db.ListSubfolders(currentParent)
			for _, sf := range subfolders {
				if sf.Name == part {
					return &FakeFileInfo{name: part, size: 0, modTime: time.Now(), isDir: true}, nil
				}
			}
			// Check files
			files, _ := fs.db.ListFilesByFolder(currentParent)
			for _, f := range files {
				if filepath.Base(f.Path) == part {
					return &FakeFileInfo{name: part, size: f.Size, modTime: time.Now(), isDir: false}, nil
				}
			}
			return nil, os.ErrNotExist
		}
		// Resolve parent folder
		subfolders, _ := fs.db.ListSubfolders(currentParent)
		found := false
		for _, sf := range subfolders {
			if sf.Name == part {
				currentParent = &sf.ID
				found = true
				break
			}
		}
		if !found { return nil, os.ErrNotExist }
	}
	return nil, os.ErrNotExist
}

// NexusFile wraps os.File to intercept Close for Uploads
type NexusFile struct {
	webdav.File
	fs       *NexusFS
	name     string
	isWrite  bool
	parentID *int64
}

func (f *NexusFile) Close() error {
	err := f.File.Close()
	if err == nil && f.isWrite {
		// Update pending size so Stat returns the correct bytes while uploading
		if info, statErr := os.Stat(filepath.Join(f.fs.cache, f.name)); statErr == nil {
			f.fs.markPending(f.name, info.Size())
		}
		log.Printf("VFS: File %s closed after write, queuing upload...", f.name)
		taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
		f.fs.queue.AddTask(&Task{
			ID:        taskID,
			Type:      TaskUpload,
			FilePath:  filepath.Join(f.fs.cache, f.name),
			Mode:      "tank",
			Status:    "Pending",
			CreatedAt: time.Now(),
			ParentID:  f.parentID,
		})
		// Clear pending once the upload task is done (async)
		go func(name string) {
			for {
				time.Sleep(2 * time.Second)
				t := f.fs.queue.GetTask(taskID)
				if t == nil || t.Status == "Completed" || t.Status == "Error" {
					f.fs.clearPending(name)
					return
				}
			}
		}(f.name)
	}
	return err
}

// NexusDir for directory listing
type NexusDir struct {
	fs       *NexusFS
	folderID *int64
}

func (d *NexusDir) Close() error               { return nil }
func (d *NexusDir) Read(p []byte) (int, error) { return 0, os.ErrInvalid }
func (d *NexusDir) Seek(offset int64, whence int) (int64, error) { return 0, os.ErrInvalid }
func (d *NexusDir) Stat() (os.FileInfo, error) { 
	if d.folderID == nil {
		return os.Stat(d.fs.cache)
	}
	f, _ := d.fs.db.GetFolderByID(*d.folderID)
	return &FakeFileInfo{name: f.Name, size: 0, modTime: time.Now(), isDir: true}, nil
}
func (d *NexusDir) Write(p []byte) (int, error) { return 0, os.ErrInvalid }
func (d *NexusDir) Readdir(count int) ([]os.FileInfo, error) {
	// List files in this folder
	files, _ := d.fs.db.ListFilesByFolder(d.folderID)
	// List subfolders
	subfolders, _ := d.fs.db.ListSubfolders(d.folderID)

	infos := []os.FileInfo{}
	
	for _, sf := range subfolders {
		infos = append(infos, &FakeFileInfo{name: sf.Name, size: 0, modTime: time.Now(), isDir: true})
	}
	for _, f := range files {
		infos = append(infos, &FakeFileInfo{name: filepath.Base(f.Path), size: f.Size, modTime: time.Now(), isDir: false})
	}
	
	return infos, nil
}

type FakeFileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

func (f *FakeFileInfo) Name() string       { return f.name }
func (f *FakeFileInfo) Size() int64        { return f.size }
func (f *FakeFileInfo) Mode() os.FileMode  { 
	if f.isDir { return 0755 | os.ModeDir }
	return 0644 
}
func (f *FakeFileInfo) ModTime() time.Time { return f.modTime }
func (f *FakeFileInfo) IsDir() bool        { return f.isDir }
func (f *FakeFileInfo) Sys() interface{}   { return nil }

func NewVFSHandler(db *Database, queue *TaskQueue) http.Handler {
	fs := NewNexusFS(db, queue)
	return &webdav.Handler{
		Prefix:     "/vfs",
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("VFS [%s]: %s -> %v", r.Method, r.URL, err)
			}
		},
	}
}
