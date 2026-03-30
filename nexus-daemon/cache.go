package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const defaultCacheMaxBytes = 10 * 1024 * 1024 * 1024 // 10 GB default

// CacheManager manages a directory of cached shard files on disk.
// It uses the database to track last-access times and sizes,
// and evicts the oldest entries when the cache exceeds MaxBytes.
type CacheManager struct {
	mu       sync.Mutex
	CacheDir string
	MaxBytes int64
	db       *Database
}

// NewCacheManager creates a CacheManager backed by a database and a
// local cache directory.  MaxBytes = 0 falls back to the 10 GB default.
func NewCacheManager(db *Database, maxBytes int64) (*CacheManager, error) {
	cacheDir := filepath.Join(os.Getenv("HOME"), ".cache", "nexus-storage", "shards")
	if err := os.MkdirAll(cacheDir, 0750); err != nil {
		return nil, fmt.Errorf("creating cache dir: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = defaultCacheMaxBytes
	}
	return &CacheManager{CacheDir: cacheDir, MaxBytes: maxBytes, db: db}, nil
}

// Get returns the path to a cached shard for the given videoID,
// or ("", false) if it is not in the cache.
func (c *CacheManager) Get(videoID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry := c.db.GetCacheEntry(videoID)
	if entry == nil {
		return "", false
	}
	// Verify the file still exists on disk
	if _, err := os.Stat(entry.FilePath); os.IsNotExist(err) {
		c.db.DeleteCacheEntry(videoID)
		return "", false
	}
	// Touch last_access
	c.db.TouchCacheEntry(videoID)
	return entry.FilePath, true
}

// Put writes data to the cache for videoID and evicts old entries if needed.
func (c *CacheManager) Put(videoID string, data []byte) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	destPath := filepath.Join(c.CacheDir, videoID+".shard")
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return "", fmt.Errorf("writing cache shard: %w", err)
	}

	size := int64(len(data))
	if err := c.db.SaveCacheEntry(videoID, destPath, size); err != nil {
		// Non-fatal: cache write failed but the shard is still on disk
		log.Printf("⚠️  cache db write failed for %s: %v", videoID, err)
	}

	c.evictIfNeeded()
	return destPath, nil
}

// Stats returns total cache size in bytes and entry count.
func (c *CacheManager) Stats() (totalBytes int64, count int) {
	return c.db.CacheStats()
}

// evictIfNeeded removes the oldest cache entries until total size is under MaxBytes.
// Must be called with c.mu held.
func (c *CacheManager) evictIfNeeded() {
	totalBytes, _ := c.db.CacheStats()
	for totalBytes > c.MaxBytes {
		entry := c.db.OldestCacheEntry()
		if entry == nil {
			break
		}
		log.Printf("♻️  Cache evict: %s (%d bytes)", entry.VideoID, entry.SizeBytes)
		os.Remove(entry.FilePath)
		c.db.DeleteCacheEntry(entry.VideoID)
		totalBytes -= entry.SizeBytes
	}
}
