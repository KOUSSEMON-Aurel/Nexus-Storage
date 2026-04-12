// nexus-daemon/archiver.go
// Handles folder-to-tarball archival for Nexus 2.0.
// Standardizes on .tar (uncompressed tar, let Rust handle Zstd).

package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ArchiveFolder tars the contents of a folder into a byte slice. (Legacy, use ArchiveFolderStream for large files)
func ArchiveFolder(root string) ([]byte, error) {
	var buf bytes.Buffer
	if err := ArchiveFolderStream(root, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ArchiveFolderStream tars the contents of a folder into an io.Writer.
func ArchiveFolderStream(root string, out io.Writer) error {
	tw := tar.NewWriter(out)

	root = filepath.Clean(root)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create header
		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}

		// Update name to be relative to root
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil // skip the root itself
		}
		header.Name = relPath

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = io.Copy(tw, file)
			if err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	return tw.Close()
}

// ExtractArchive untars a byte slice into a destination directory. (Legacy)
func ExtractArchive(data []byte, dest string) error {
	return ExtractArchiveStream(bytes.NewReader(data), dest)
}

// ExtractArchiveStream untars data from an io.Reader into a destination directory.
func ExtractArchiveStream(r io.Reader, dest string) error {
	tr := tar.NewReader(r)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(dest, header.Name)

		// Check for ZipSlip vulnerability
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != dest {
			return fmt.Errorf("invalid file path in archive: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}
