package analyzer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
)

// binarySniffLen matches git's own heuristic: a NUL in the first 8000 bytes.
const binarySniffLen = 8000

// HumanReadableSize converts a byte count into a human-readable string
// using binary units (1024-based: KB, MB, GB).
func HumanReadableSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)

	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// IsLikelyBinary reads up to the first 8000 bytes of the file at filePath
// and returns true if the sample contains a null byte (0x00).
func IsLikelyBinary(filePath string) (bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, binarySniffLen)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}
	return ContainsNUL(buf[:n]), nil
}

// ContainsNUL reports whether the first 8000 bytes of b contain a NUL byte.
func ContainsNUL(b []byte) bool {
	if len(b) > binarySniffLen {
		b = b[:binarySniffLen]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// GetFileSize returns the size in bytes of the file at filePath. Symlinks are
// not followed: git stores a symlink as its target path, so that is its size.
func GetFileSize(filePath string) (int64, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
