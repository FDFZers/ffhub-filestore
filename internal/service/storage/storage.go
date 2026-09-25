package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

var StorageDir = "./storage"
var FileDir = filepath.Join(StorageDir, "files")

func EnsureDir() error {
	if err := os.MkdirAll(FileDir, 0o755); err != nil {
		return fmt.Errorf("storage: create dir: %w", err)
	}
	return nil
}

func FilePath(sha512 string) string {
	return filepath.Join(FileDir, sha512[:2], sha512)
}
