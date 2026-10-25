// Package secretfiles reads executor credentials from one trusted read-only
// mounted directory. It has no HTTP routes and never stores resolved values.
package secretfiles

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const MaxBytes = 64 << 10

var ErrUnavailable = errors.New("managed secret is unavailable")

type Directory struct{ root *os.Root }

// ValidKey accepts a single visible filename, never a path or projection control
// entry such as ..data. Catalog writers and the reader use the same rule.
func ValidKey(key string) bool {
	if len(key) == 0 || len(key) > 128 || key[0] == '.' {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// Open selects an explicit workspace directory beneath the operator-owned base.
// Mounted files for different workspaces must never share that child directory.
func Open(directory, workspaceID string) (*Directory, error) {
	if !filepath.IsAbs(directory) || len(workspaceID) != 32 || strings.ToLower(workspaceID) != workspaceID {
		return nil, ErrUnavailable
	}
	if _, err := hex.DecodeString(workspaceID); err != nil {
		return nil, ErrUnavailable
	}
	base, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer base.Close()
	info, err := base.Lstat(workspaceID)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnavailable
	}
	root, err := base.OpenRoot(workspaceID)
	if err != nil {
		return nil, ErrUnavailable
	}
	return &Directory{root: root}, nil
}
func (d *Directory) Close() error {
	if d.root.Close() != nil {
		return ErrUnavailable
	}
	return nil
}

// Read follows projected-volume symlinks within the opened root. os.Root
// prevents escape even if a symlink changes. No value or filename enters errors.
// The mount must be trusted and read-only to the application, as with a normal
// Kubernetes Secret volume. Values retain exact bytes, including newlines.
func (d *Directory) Read(key string) ([]byte, error) {
	if !ValidKey(key) {
		return nil, ErrUnavailable
	}
	info, err := d.root.Stat(key)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, ErrUnavailable
	}
	file, err := d.root.Open(key)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxBytes {
		return nil, ErrUnavailable
	}
	value, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil || len(value) == 0 || len(value) > MaxBytes {
		return nil, ErrUnavailable
	}
	return value, nil
}
