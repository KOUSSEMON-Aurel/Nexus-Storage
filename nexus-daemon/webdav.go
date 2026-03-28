package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/webdav"
)

// NexusFS implements webdav.FileSystem
type NexusFS struct {
	db    *Database
	queue *TaskQueue
	cache string
}

func NewNexusFS(db *Database, queue *TaskQueue) *NexusFS {
	home, _ := os.UserHomeDir()
	cache := filepath.Join(home, ".nexus", "cache")
	os.MkdirAll(cache, 0755)
	return &NexusFS{db: db, queue: queue, cache: cache}
}

func (fs *NexusFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	// Nexus is flat for now, but we'll allow mkdir for compatibility
	return os.MkdirAll(filepath.Join(fs.cache, name), perm)
}

func (fs *NexusFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	cleanName := strings.TrimPrefix(name, "/")
	if cleanName == "" {
		return &NexusDir{fs: fs, path: "/"}, nil
	}

	// Check if it's a known file in DB
	files, _ := fs.db.ListFiles()
	var record *FileRecord
	for _, f := range files {
		if filepath.Base(f.Path) == cleanName {
			record = &f
			break
		}
	}

	fullPath := filepath.Join(fs.cache, cleanName)

	// If opening for writing
	if flag&os.O_WRONLY != 0 || flag&os.O_RDWR != 0 || flag&os.O_CREATE != 0 {
		f, err := os.OpenFile(fullPath, flag, perm)
		if err != nil {
			return nil, err
		}
		return &NexusFile{File: f, fs: fs, name: cleanName, isWrite: true}, nil
	}

	// Opening for reading
	if _, err := os.Stat(fullPath); os.IsNotExist(err) && record != nil {
		// Trigger background download if not in cache
		log.Printf("WebDAV: File %s not in cache, triggering download...", cleanName)
		fs.queue.AddTask(&Task{
			ID:        record.VideoID,
			Type:      TaskDownload,
			FilePath:  cleanName,
			Status:    "Pending",
			CreatedAt: time.Now(),
		})
		// We can't easily block WebDAV here without UI freeze, 
		// but since it's "Réseau-Local", standard clients will retry or wait.
		return nil, os.ErrNotExist 
	}

	f, err := os.OpenFile(fullPath, flag, perm)
	if err != nil {
		return nil, err
	}
	return &NexusFile{File: f, fs: fs, name: cleanName, isWrite: false}, nil
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
	// We prefer the cache stat if available, otherwise fake it from DB
	fi, err := os.Stat(filepath.Join(fs.cache, cleanName))
	if err == nil {
		return fi, nil
	}

	files, _ := fs.db.ListFiles()
	for _, f := range files {
		if filepath.Base(f.Path) == cleanName {
			return &FakeFileInfo{name: cleanName, size: f.Size, modTime: time.Now()}, nil
		}
	}
	return nil, os.ErrNotExist
}

// NexusFile wraps os.File to intercept Close for Uploads
type NexusFile struct {
	webdav.File
	fs      *NexusFS
	name    string
	isWrite bool
}

func (f *NexusFile) Close() error {
	err := f.File.Close()
	if err == nil && f.isWrite {
		log.Printf("WebDAV: File %s closed after write, queuing upload...", f.name)
		f.fs.queue.AddTask(&Task{
			ID:        fmt.Sprintf("task-%d", time.Now().UnixNano()),
			Type:      TaskUpload,
			FilePath:  filepath.Join(f.fs.cache, f.name),
			Mode:      "tank",
			Status:    "Pending",
			CreatedAt: time.Now(),
		})
	}
	return err
}

// NexusDir for directory listing
type NexusDir struct {
	fs   *NexusFS
	path string
}

func (d *NexusDir) Close() error               { return nil }
func (d *NexusDir) Read(p []byte) (int, error) { return 0, os.ErrInvalid }
func (d *NexusDir) Seek(offset int64, whence int) (int64, error) { return 0, os.ErrInvalid }
func (d *NexusDir) Stat() (os.FileInfo, error) { return os.Stat(d.fs.cache) }
func (d *NexusDir) Write(p []byte) (int, error) { return 0, os.ErrInvalid }
func (d *NexusDir) Readdir(count int) ([]os.FileInfo, error) {
	// Merge local cache files and DB files
	files, _ := d.fs.db.ListFiles()
	infos := []os.FileInfo{}
	seen := make(map[string]bool)

	// Add local files
	entries, _ := os.ReadDir(d.fs.cache)
	for _, e := range entries {
		info, _ := e.Info()
		infos = append(infos, info)
		seen[e.Name()] = true
	}

	// Add virtual files from DB
	for _, f := range files {
		name := filepath.Base(f.Path)
		if !seen[name] {
			infos = append(infos, &FakeFileInfo{name: name, size: f.Size, modTime: time.Now()})
		}
	}
	return infos, nil
}

type FakeFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (f *FakeFileInfo) Name() string       { return f.name }
func (f *FakeFileInfo) Size() int64        { return f.size }
func (f *FakeFileInfo) Mode() os.FileMode  { return 0644 }
func (f *FakeFileInfo) ModTime() time.Time { return f.modTime }
func (f *FakeFileInfo) IsDir() bool        { return false }
func (f *FakeFileInfo) Sys() interface{}   { return nil }

func NewWebDAVHandler(db *Database, queue *TaskQueue) http.Handler {
	fs := NewNexusFS(db, queue)
	return &webdav.Handler{
		Prefix:     "/webdav",
		FileSystem: fs,
		LockSystem: webdav.NewMemLS(),
		Logger: func(r *http.Request, err error) {
			if err != nil {
				log.Printf("WebDAV [%s]: %s -> %v", r.Method, r.URL, err)
			}
		},
	}
}
