package fileutils

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"ffhub-filestore/internal/service/filemeta"
	"ffhub-filestore/internal/service/storage"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func(in *os.File) {
		err := in.Close()
		if err != nil {
			slog.Warn("Failed to close file", "error", err)
		}
	}(in)

	out, err := os.CreateTemp(filepath.Dir(dst), ".dst-*")
	if err != nil {
		return err
	}
	tmpOut := out.Name()

	if _, err := io.Copy(out, in); err != nil {
		err := out.Close()
		if err != nil {
			slog.Warn("Failed to close temp file", "error", err)
		}
		err = os.Remove(tmpOut)
		if err != nil {
			slog.Warn("Failed to remove temp file", "error", err)
		}
		return err
	}
	if err := out.Sync(); err != nil {
		err := out.Close()
		if err != nil {
			slog.Warn("Failed to close temp file", "error", err)
		}
		err = os.Remove(tmpOut)
		if err != nil {
			slog.Warn("Failed to remove temp file", "error", err)
		}
		return err
	}
	if err := out.Close(); err != nil {
		err := os.Remove(tmpOut)
		if err != nil {
			slog.Warn("Failed to remove temp file", "error", err)
		}
		return err
	}
	return os.Rename(tmpOut, dst)
}

func MoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		if err := CopyFile(src, dst); err != nil {
			return fmt.Errorf("copy to dst: %w", err)
		}
		if err := os.Remove(src); err != nil {
			slog.Warn("Failed to remove temp file", "error", err)
		}
	}
	return nil
}

func ComputeSHA512(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			slog.Warn("Failed to close file", "error", err)
		}
	}(f)

	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func DetectContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			slog.Warn("Failed to close file", "error", err)
		}
	}(f)

	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", err
	}
	return http.DetectContentType(head[:n]), nil
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

func SafeJoin(base, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty path")
	}
	if strings.ContainsRune(rel, 0) {
		return "", errors.New("NUL byte in path")
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") ||
		(len(rel) >= 2 && rel[1] == ':') {
		return "", errors.New("absolute path not allowed")
	}

	cleanBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(cleanBase, filepath.FromSlash(rel))
	cleanJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}

	relCheck, err := filepath.Rel(cleanBase, cleanJoined)
	if err != nil || relCheck == ".." ||
		strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes base directory")
	}
	return cleanJoined, nil
}
