// nexus-daemon/recovery.go
// V4 Security: Encrypted manifest backup/restore system
// Handles building recoverable manifest, encrypting with masterKey, and backing up to Drive

package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"google.golang.org/api/drive/v3"
)

// ─── Manifest Data Structures ─────────────────────────────────────────────────

// EncryptedManifestPacket is what's stored on Drive (encrypted payload + public salt)
type EncryptedManifestPacket struct {
	Version               string `json:"version"`               // "v4"
	RecoverySalt          string `json:"recovery_salt"`         // hex, 16 bytes, PUBLIC
	EncryptedManifestData string `json:"encrypted_manifest"`    // hex, encrypted
}

// DecryptedManifest is the plaintext structure (encrypted with masterKey for storage)
type DecryptedManifest struct {
	Version    string       `json:"version"`     // "v4"
	CreatedTS  string       `json:"created_ts"`  // RFC3339
	Revision   int          `json:"revision"`    // incremented on password rotation
	Files      []FileEntry  `json:"files"`
	UploadTS   string       `json:"upload_ts"`   // last manifest write to Drive
}

// FileEntry represents a single file in the manifest
type FileEntry struct {
	FileID           int64        `json:"file_id"`
	SHA256           string       `json:"sha256"`
	FileName         string       `json:"file_name"`
	VideoID          string       `json:"video_id"`           // primary YouTube shard
	FileKeyEncrypted string       `json:"file_key_encrypted"` // hex, encrypted with masterKey
	Status           string       `json:"status"`             // "pending" | "confirmed"
	CreatedAt        string       `json:"created_at"`
	Shards           []ShardEntry `json:"shards"`
}

// ShardEntry represents a single shard video on YouTube
type ShardEntry struct {
	YoutubeID string `json:"youtube_id"`
	Index     int    `json:"index"`
	UploadedAt string `json:"uploaded_at"`
}

// ─── Recovery Service ─────────────────────────────────────────────────────────

// BuildDecryptedManifest assembles the current state into a DecryptedManifest
func (q *TaskQueue) BuildDecryptedManifest() (*DecryptedManifest, error) {
	// Get all non-deleted files
	files, err := q.db.ListFiles()
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	manifest := &DecryptedManifest{
		Version:   "v4",
		CreatedTS: time.Now().Format(time.RFC3339),
		Revision:  1,
		Files:     []FileEntry{},
		UploadTS:  time.Now().Format(time.RFC3339),
	}

	// Get manifest revision from DB
	rev, err := q.db.GetManifestRevision()
	if err == nil {
		manifest.Revision = rev
	}

	// Convert each file
	for _, f := range files {
		if f.VideoID == "" {
			continue // Skip files not yet uploaded
		}

		// Fetch shards for this file
		shardIDs, err := q.db.GetShardsForFile(f.ID)
		if err != nil {
			log.Printf("⚠️  Could not fetch shards for file %d: %v", f.ID, err)
			continue
		}

		shards := []ShardEntry{}
		for i, shardID := range shardIDs {
			shards = append(shards, ShardEntry{
				YoutubeID:  shardID,
				Index:      i + 1, // 1-indexed
				UploadedAt: time.Now().Format(time.RFC3339), // TODO: track actual upload time
			})
		}

		entry := FileEntry{
			FileID:           f.ID,
			SHA256:           f.SHA256,
			FileName:         f.Path,
			VideoID:          f.VideoID,
			FileKeyEncrypted: f.FileKey, // Already encrypted (hex)
			Status:           "confirmed", // Default; upload flow updates to "pending" first
			CreatedAt:        f.LastUpdate,
			Shards:           shards,
		}
		manifest.Files = append(manifest.Files, entry)
	}

	return manifest, nil
}

// EncryptAndBackupManifest builds manifest, encrypts with masterKey, and backs up to Drive
func (q *TaskQueue) EncryptAndBackupManifest(masterKeyHex string) error {
	// Decode masterKey from hex
	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return fmt.Errorf("invalid masterKey hex: %w", err)
	}

	if len(masterKey) != 32 {
		return fmt.Errorf("invalid masterKey length: expected 32, got %d", len(masterKey))
	}

	// 1. Build plaintext manifest
	manifest, err := q.BuildDecryptedManifest()
	if err != nil {
		return fmt.Errorf("failed to build manifest: %w", err)
	}

	// 2. Serialize manifest to JSON
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	// 3. Encrypt manifest with masterKey
	// Use EncryptWithKey directly with the [32]byte array
	keyArray := [32]byte{}
	copy(keyArray[:], masterKey)
	encryptedManifest, err := q.core.EncryptWithKey(manifestJSON, keyArray[:])
	if err != nil {
		return fmt.Errorf("failed to encrypt manifest: %w", err)
	}

	// 4. Get recovery salt from DB
	saltHex, err := q.db.GetRecoverySalt()
	if err != nil {
		return fmt.Errorf("failed to get recovery salt: %w", err)
	}

	if saltHex == "" {
		// First time: generate and store salt
		saltBytes, err := q.core.GenerateRecoverySalt()
		if err != nil {
			return fmt.Errorf("failed to generate recovery salt: %w", err)
		}
		saltHex = hex.EncodeToString(saltBytes)
		if err := q.db.SetRecoverySalt(saltHex); err != nil {
			return fmt.Errorf("failed to store recovery salt: %w", err)
		}
	}

	// 5. Build packet (salt is public, manifest is encrypted)
	packet := EncryptedManifestPacket{
		Version:               "v4",
		RecoverySalt:          saltHex,
		EncryptedManifestData: hex.EncodeToString(encryptedManifest),
	}

	// 6. Serialize packet to JSON
	packetJSON, err := json.MarshalIndent(packet, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal packet: %w", err)
	}

	// 7. Upload to Drive as "nexus-recovery-v4.json"
	if q.pm == nil {
		log.Printf("⚠️  PlaylistManager not available, skipping Drive backup")
		return nil
	}

	driveID, err := q.pm.BackupRecoveryPacketToDrive(packetJSON)
	if err != nil {
		return fmt.Errorf("failed to backup to Drive: %w", err)
	}

	// 8. Store the Drive file ID in database for future recovery downloads
	if err := q.db.SetRecoveryPacketDriveID(driveID); err != nil {
		log.Printf("⚠️  Failed to store recovery packet Drive ID: %v", err)
	}

	// 9. Record backup timestamp
	if err := q.db.SetLastManifestBackup(time.Now().Format(time.RFC3339)); err != nil {
		log.Printf("⚠️  Failed to record backup timestamp: %v", err)
	}

	log.Printf("✅ Manifest encrypted and backed up to Drive: %s", driveID)
	return nil
}

// RestoreManifestFromDrive downloads and decrypts manifest from Drive
// Returns the DecryptedManifest for inspection/recovery
func (q *TaskQueue) RestoreManifestFromDrive(masterKeyHex string) (*DecryptedManifest, error) {
	// Decode masterKey
	masterKey, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid masterKey hex: %w", err)
	}

	if len(masterKey) != 32 {
		return nil, fmt.Errorf("invalid masterKey length: expected 32, got %d", len(masterKey))
	}

	if q.pm == nil {
		return nil, fmt.Errorf("PlaylistManager not available")
	}

	// 1. Download recovery packet from Drive
	packetJSON, err := q.pm.DownloadRecoveryPacketFromDrive()
	if err != nil {
		return nil, fmt.Errorf("failed to download from Drive: %w", err)
	}

	// 2. Parse packet
	var packet EncryptedManifestPacket
	if err := json.Unmarshal(packetJSON, &packet); err != nil {
		return nil, fmt.Errorf("failed to parse packet: %w", err)
	}

	if packet.Version != "v4" {
		return nil, fmt.Errorf("unsupported manifest version: %s", packet.Version)
	}

	// 3. Decode encrypted manifest from hex
	encryptedData, err := hex.DecodeString(packet.EncryptedManifestData)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted manifest: %w", err)
	}

	// 4. Decrypt with masterKey
	keyArray := [32]byte{}
	copy(keyArray[:], masterKey)
	decryptedJSON, err := q.core.DecryptWithKey(encryptedData, keyArray[:])
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt manifest (wrong password?): %w", err)
	}

	// 5. Parse manifest
	var manifest DecryptedManifest
	if err := json.Unmarshal(decryptedJSON, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse decrypted manifest: %w", err)
	}

	return &manifest, nil
}

// ApplyRestoredManifestToDB writes recovered manifest back to the database
func (q *TaskQueue) ApplyRestoredManifestToDB(manifest *DecryptedManifest) error {
	for _, entry := range manifest.Files {
		// Recreate file record
		err := q.db.SaveFileWithKey(
			entry.FileName,
			entry.VideoID,
			0, // Size: not critical for recovery
			"recovery",
			"recovery",
			nil, // parentID
			entry.SHA256,
			entry.FileKeyEncrypted, // The encrypted file_key
			false, // is_archive
		)
		if err != nil {
			log.Printf("⚠️  Failed to restore file %s: %v", entry.FileName, err)
			continue
		}

		// Get the file ID we just inserted
		file, err := q.db.GetFileByHash(entry.SHA256)
		if err != nil || file == nil {
			log.Printf("⚠️  Could not fetch restored file: %s", entry.FileName)
			continue
		}

		// Restore shards
		for _, shard := range entry.Shards {
			q.db.SaveShard(file.ID, shard.YoutubeID, int(shard.Index-1))
		}
	}

	log.Printf("✅ Restored %d files from manifest", len(manifest.Files))
	return nil
}

// BackupRecoveryPacketToDrive uploads the encrypted manifest packet to Drive
// Returns the Drive file ID
func (pm *PlaylistManager) BackupRecoveryPacketToDrive(packetJSON []byte) (string, error) {
	if pm == nil || pm.yt == nil {
		return "", fmt.Errorf("PlaylistManager or YouTubeManager not available")
	}

	driveSvc := pm.yt.GetDriveService()
	if driveSvc == nil {
		return "", fmt.Errorf("Drive service not initialized - authentication required")
	}

	// Step 1: Find or create recovery folder
	folderID, err := pm.getRecoveryFolderID()
	if err != nil {
		log.Printf("⚠️  Warning: Could not find recovery folder: %v", err)
		folderID = "" // Proceed without folder organization
	}

	// Step 2: Search for existing recovery packet file to overwrite
	query := "name = 'recovery-packet.json' and trashed = false"
	if folderID != "" {
		query = fmt.Sprintf("name = 'recovery-packet.json' and '%s' in parents and trashed = false", folderID)
	}

	fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err == nil && len(fileList.Files) > 0 {
		// Overwrite existing recovery packet
		fileID := fileList.Files[0].Id
		_, err := driveSvc.Files.Update(fileID, nil).Media(bytes.NewReader(packetJSON)).Do()
		if err != nil {
			return "", fmt.Errorf("failed to update recovery packet: %w", err)
		}
		log.Printf("📦 Recovery packet updated on Drive (file_id: %s, %d bytes)", fileID, len(packetJSON))
		return fileID, nil
	}

	// Step 3: Create new recovery packet file
	f := &drive.File{
		Name:     "recovery-packet.json",
		MimeType: "application/json",
	}
	if folderID != "" {
		f.Parents = []string{folderID}
	}

	res, err := driveSvc.Files.Create(f).Media(bytes.NewReader(packetJSON)).Do()
	if err != nil {
		return "", fmt.Errorf("failed to create recovery packet: %w", err)
	}

	log.Printf("📦 Recovery packet uploaded to Drive (file_id: %s, %d bytes)", res.Id, len(packetJSON))
	return res.Id, nil
}

// DownloadRecoveryPacketFromDrive downloads the encrypted manifest packet from Drive
func (pm *PlaylistManager) DownloadRecoveryPacketFromDrive() ([]byte, error) {
	if pm == nil || pm.yt == nil || pm.db == nil {
		return nil, fmt.Errorf("PlaylistManager, YouTubeManager, or Database not available")
	}

	driveSvc := pm.yt.GetDriveService()
	if driveSvc == nil {
		return nil, fmt.Errorf("Drive service not initialized - authentication required")
	}

	// Step 1: Get the stored recovery packet file ID from database
	fileID, err := pm.db.GetRecoveryPacketDriveID()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve recovery packet ID from database: %w", err)
	}

	// Step 2: If no file ID in DB, search for recovery packet
	if fileID == "" {
		query := "name = 'recovery-packet.json' and trashed = false"
		fileList, err := driveSvc.Files.List().Q(query).Fields("files(id)").PageSize(1).Do()
		if err != nil {
			return nil, fmt.Errorf("failed to search for recovery packet: %w", err)
		}
		if len(fileList.Files) == 0 {
			return nil, fmt.Errorf("recovery packet not found on Drive - no previous backup")
		}
		fileID = fileList.Files[0].Id
		// Update DB with the found file ID for future use
		pm.db.SetRecoveryPacketDriveID(fileID)
	}

	// Step 3: Download the recovery packet
	resp, err := driveSvc.Files.Get(fileID).Download()
	if err != nil {
		return nil, fmt.Errorf("failed to download recovery packet from Drive: %w", err)
	}
	defer resp.Body.Close()

	// Step 4: Read the entire response body
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read recovery packet data: %w", err)
	}

	log.Printf("📦 Recovery packet downloaded from Drive (file_id: %s, %d bytes)", fileID, len(data))
	return data, nil
}

// getRecoveryFolderID finds or creates a dedicated recovery folder on Drive
func (pm *PlaylistManager) getRecoveryFolderID() (string, error) {
	if pm == nil || pm.yt == nil {
		return "", fmt.Errorf("PlaylistManager not available")
	}

	driveSvc := pm.yt.GetDriveService()
	if driveSvc == nil {
		return "", fmt.Errorf("Drive service not initialized")
	}

	// Search for existing recovery folder
	query := "name = 'Nexus-Recovery' and mimeType = 'application/vnd.google-apps.folder' and trashed = false"
	list, err := driveSvc.Files.List().Q(query).Fields("files(id)").Do()
	if err == nil && len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	// Create recovery folder if not found
	f := &drive.File{
		Name:     "Nexus-Recovery",
		MimeType: "application/vnd.google-apps.folder",
	}
	res, err := driveSvc.Files.Create(f).Do()
	if err != nil {
		return "", fmt.Errorf("failed to create recovery folder: %w", err)
	}

	log.Printf("📁 Created recovery folder on Drive (folder_id: %s)", res.Id)
	return res.Id, nil
}
