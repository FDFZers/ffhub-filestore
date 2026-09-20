package fileutils

import (
	"io"
	"os"
	"path/filepath"
)

func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.CreateTemp(filepath.Dir(dst), ".dst-*")
	if err != nil {
		return err
	}
	tmpOut := out.Name()

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmpOut)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmpOut)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmpOut)
		return err
	}
	return os.Rename(tmpOut, dst)
}
