package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

var StorageDir = "./storage"
var FileDir = filepath.Join(StorageDir, "files")
var TempDir = filepath.Join(StorageDir, "tmp")

func EnsureDir() error {
	if err := os.MkdirAll(FileDir, 0o755); err != nil {
		return fmt.Errorf("storage: create dir: %w", err)
	}
	if err := os.MkdirAll(TempDir, 0o755); err != nil {
		return fmt.Errorf("storage: create dir: %w", err)
	}
	return nil
}

func FilePath(sha512 string) string {
	if len(sha512) <= 2 {
		return filepath.Join(FileDir, "other", sha512)
	}
	return filepath.Join(FileDir, sha512[:2], sha512)
}
