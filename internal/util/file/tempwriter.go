package fileutils

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"ffhub-filestore/internal/service/filemeta"
	"ffhub-filestore/internal/service/storage"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

var ErrSHA512Mismatch = errors.New("sha512 mismatch")

type TempWriter struct {
	tmpFile   *os.File
	tmpPath   string
	dstPath   string
	dir       string
	committed bool
	closed    bool

	hasher       hash.Hash
	expectedHash string
	actualHash   string
}

func NewTempWriter(dstPath string, sha512Sum string) (*TempWriter, error) {
	if dstPath == "" {
		return nil, errors.New("dstPath is empty")
	}

	dir := filepath.Dir(dstPath)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}

	f, err := os.CreateTemp(storage.TempDir, "upload-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}

	return &TempWriter{
		tmpFile:      f,
		tmpPath:      f.Name(),
		dstPath:      dstPath,
		dir:          dir,
		hasher:       sha512.New(),
		expectedHash: sha512Sum,
	}, nil
}

func (w *TempWriter) SHA512Sum() string {
	return hex.EncodeToString(w.hasher.Sum(nil))
}

func (w *TempWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("writer already closed")
	}
	n, err := w.tmpFile.Write(p)
	if n > 0 {
		if _, err = w.hasher.Write(p[:n]); err != nil {
			slog.Warn("Failed to write to hasher", "error", err)
		}
	}
	return n, err
}

func (w *TempWriter) WriteFrom(r io.Reader) (int64, error) {
	if w.closed {
		return 0, errors.New("writer already closed")
	}
	buf := make([]byte, 32*1024)
	mw := io.MultiWriter(w.tmpFile, w.hasher)
	return io.CopyBuffer(mw, r, buf)
}

func (w *TempWriter) Commit() error {
	if w.committed {
		return errors.New("already committed")
	}
	if w.closed {
		return errors.New("writer already closed")
	}

	w.actualHash = w.SHA512Sum()

	if w.expectedHash != "" && w.expectedHash != w.actualHash {
		if err := w.tmpFile.Close(); err != nil {
			slog.Warn("Failed to close temp file", "error", err)
		}
		w.closed = true
		if err := os.Remove(w.tmpPath); err != nil {
			slog.Warn("Failed to remove temp file", "error", err)
		}
		return fmt.Errorf("%w: expected %s, got %s", ErrSHA512Mismatch, w.expectedHash, w.actualHash)
	}

	if err := w.tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := w.tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	w.closed = true

	if err := os.Rename(w.tmpPath, w.dstPath); err != nil {
		if err := CopyFile(w.tmpPath, w.dstPath); err != nil {
			return fmt.Errorf("copy to dst: %w", err)
		}
		if err := os.Remove(w.tmpPath); err != nil {
			slog.Warn("Failed to remove temp file", "error", err)
		}
	}

	w.committed = true
	return nil
}

func (w *TempWriter) Abort() {
	if w.committed {
		return
	}
	if !w.closed {
		if err := w.tmpFile.Close(); err != nil {
			slog.Warn("Failed to close temp file", "error", err)
		}
		w.closed = true
	}
	if err := os.Remove(w.tmpPath); err != nil {
		slog.Warn("Failed to remove temp file", "error", err)
	}
}

func RemoveOrphanBlob(sha512 string) {
	path := storage.FilePath(sha512)
	if path == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	referenced, err := filemeta.CheckSHA512Exists(ctx, sha512)
	if err != nil {
		slog.Warn("Failed to check blob reference", "error", err, "sha512", sha512)
		return
	}
	if referenced {
		return
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("Failed to remove orphan blob", "error", err, "sha512", sha512)
	}
}
