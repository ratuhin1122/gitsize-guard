package analyzer

import (
	"fmt"
	"io"
	"os"
)

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

	buf := make([]byte, 8000)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}

	for i := 0; i < n; i++ {
		if buf[i] == 0x00 {
			return true, nil
		}
	}
	return false, nil
}

// GetFileSize returns the size in bytes of the file at filePath.
func GetFileSize(filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
