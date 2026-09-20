package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHumanReadableSize(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1023, "1023 B"},
		{2048, "2.0 KB"},
		{180 * 1024 * 1024, "180.0 MB"},
		{2 * 1024 * 1024 * 1024, "2.0 GB"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			got := HumanReadableSize(tc.bytes)
			if got != tc.expected {
				t.Errorf("HumanReadableSize(%d) = %q, want %q", tc.bytes, got, tc.expected)
			}
		})
	}
}

func TestIsLikelyBinary(t *testing.T) {
	dir := t.TempDir()

	t.Run("text file returns false", func(t *testing.T) {
		path := filepath.Join(dir, "text.txt")
		if err := os.WriteFile(path, []byte("hello world\nline two\n"), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := IsLikelyBinary(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got {
			t.Error("expected false for text file, got true")
		}
	})

	t.Run("binary file returns true", func(t *testing.T) {
		path := filepath.Join(dir, "binary.bin")
		data := []byte("some data\x00more data")
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
		got, err := IsLikelyBinary(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got {
			t.Error("expected true for binary file, got false")
		}
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		_, err := IsLikelyBinary(filepath.Join(dir, "nope.txt"))
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})
}

func TestGetFileSize(t *testing.T) {
	dir := t.TempDir()

	t.Run("known size file", func(t *testing.T) {
		path := filepath.Join(dir, "sized.dat")
		content := make([]byte, 4096)
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatal(err)
		}
		got, err := GetFileSize(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 4096 {
			t.Errorf("GetFileSize() = %d, want 4096", got)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(dir, "empty.dat")
		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			t.Fatal(err)
		}
		got, err := GetFileSize(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != 0 {
			t.Errorf("GetFileSize() = %d, want 0", got)
		}
	})

	t.Run("nonexistent file returns error", func(t *testing.T) {
		_, err := GetFileSize(filepath.Join(dir, "nope.dat"))
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})
}
