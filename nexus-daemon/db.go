package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

type Database struct {
	db *sql.DB
}

type FileRecord struct {
	ID         int64
	Path       string
	VideoID    string
	Size       int64
	Hash       string
	Key        string
	LastUpdate string
}

func (d *Database) Init(dbPath string) error {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("could not open database: %w", err)
	}
	d.db = db

	// Initialize tables
	query := `
	CREATE TABLE IF NOT EXISTS files (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT UNIQUE,
		video_id TEXT,
		size INTEGER,
		hash TEXT,
		key TEXT,
		last_update TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	`
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("could not create tables: %w", err)
	}

	return nil
}

func (d *Database) Close() {
	if d.db != nil {
		d.db.Close()
	}
}

func (d *Database) SaveFile(path, videoID string, size int64, hash, key string) error {
	query := `
	INSERT INTO files (path, video_id, size, hash, key)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(path) DO UPDATE SET
		video_id = excluded.video_id,
		size = excluded.size,
		hash = excluded.hash,
		key = excluded.key,
		last_update = CURRENT_TIMESTAMP;
	`
	if _, err := d.db.Exec(query, path, videoID, size, hash, key); err != nil {
		return fmt.Errorf("could not save file: %w", err)
	}
	return nil
}

func (d *Database) GetFile(path string) (*FileRecord, error) {
	row := d.db.QueryRow("SELECT id, path, video_id, size, hash, key, last_update FROM files WHERE path = ?", path)
	var fr FileRecord
	if err := row.Scan(&fr.ID, &fr.Path, &fr.VideoID, &fr.Size, &fr.Hash, &fr.Key, &fr.LastUpdate); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, err
	}
	return &fr, nil
}
