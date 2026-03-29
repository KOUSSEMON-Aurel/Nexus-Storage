package main

import (
	"fmt"
	"log"

	"google.golang.org/api/youtube/v3"
)

type PlaylistManager struct {
	yt *YouTubeManager
	db *Database
}

func NewPlaylistManager(yt *YouTubeManager, db *Database) *PlaylistManager {
	return &PlaylistManager{yt: yt, db: db}
}

// EnsureBasePlaylists creates NEXUS_ROOT and NEXUS_MANIFEST if they don't exist.
func (pm *PlaylistManager) EnsureBasePlaylists() error {
	svc := pm.yt.GetService()
	if svc == nil {
		return fmt.Errorf("YouTube service not available")
	}

	call := svc.Playlists.List([]string{"snippet", "id"}).Mine(true).MaxResults(50)
	resp, err := call.Do()
	if err != nil {
		return err
	}
	pm.db.LogQuotaUsage(1)

	foundRoot := ""
	foundManifest := ""

	for _, p := range resp.Items {
		if p.Snippet.Title == "NEXUS_ROOT" {
			foundRoot = p.Id
		} else if p.Snippet.Title == "NEXUS_MANIFEST" {
			foundManifest = p.Id
		}
	}

	if foundRoot == "" {
		p, err := pm.CreatePlaylist("NEXUS_ROOT", "Nexus-Storage Root Shards")
		if err != nil {
			return err
		}
		foundRoot = p.Id
	}
	if foundManifest == "" {
		p, err := pm.CreatePlaylist("NEXUS_MANIFEST", "Nexus-Storage Database Backups")
		if err != nil {
			return err
		}
		foundManifest = p.Id
	}

	pm.db.SetKV("playlist_root_id", foundRoot)
	pm.db.SetKV("playlist_manifest_id", foundManifest)
	return nil
}

func (pm *PlaylistManager) CreatePlaylist(title, description string) (*youtube.Playlist, error) {
	svc := pm.yt.GetService()
	p := &youtube.Playlist{
		Snippet: &youtube.PlaylistSnippet{
			Title:       title,
			Description: description,
		},
		Status: &youtube.PlaylistStatus{
			PrivacyStatus: "unlisted",
		},
	}
	
	res, err := svc.Playlists.Insert([]string{"snippet", "status"}, p).Do()
	if err == nil {
		pm.db.LogQuotaUsage(50)
		log.Printf("🎬 Created Playlist: %s (%s)", title, res.Id)
	}
	return res, err
}

func (pm *PlaylistManager) SyncFolderToPlaylist(folderID int64) (string, error) {
	folder, err := pm.db.GetFolderByID(folderID)
	if err != nil || folder == nil {
		return "", err
	}

	if folder.PlaylistID != nil && *folder.PlaylistID != "" {
		return *folder.PlaylistID, nil
	}

	title := "NEXUS_" + folder.Name
	p, err := pm.CreatePlaylist(title, "Nexus-Storage Folder: "+folder.Name)
	if err != nil {
		return "", err
	}

	_, err = pm.db.db.Exec(`UPDATE folders SET playlist_id = ? WHERE id = ?`, p.Id, folderID)
	return p.Id, err
}

func (pm *PlaylistManager) AddVideoToPlaylist(playlistID, videoID string) error {
	svc := pm.yt.GetService()
	item := &youtube.PlaylistItem{
		Snippet: &youtube.PlaylistItemSnippet{
			PlaylistId: playlistID,
			ResourceId: &youtube.ResourceId{
				Kind:    "youtube#video",
				VideoId: videoID,
			},
		},
	}
	_, err := svc.PlaylistItems.Insert([]string{"snippet"}, item).Do()
	if err == nil {
		pm.db.LogQuotaUsage(50)
		log.Printf("📎 Added Video %s to Playlist %s", videoID, playlistID)
	}
	return err
}
