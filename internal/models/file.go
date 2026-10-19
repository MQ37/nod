package models

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
)

const unpinned = "TODO-sha256"

// File is one artifact with the size and sha256 published for it.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Pinned reports whether the file has a usable sha256.
func (f File) Pinned() bool {
	return len(f.SHA256) == 64 && f.SHA256 != unpinned && !strings.Contains(f.SHA256, "TODO")
}

// HashFile returns the hex sha256 of a file.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
