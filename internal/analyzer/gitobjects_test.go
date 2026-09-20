package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGitObjects(t *testing.T) {
	// Skip if git is not available on PATH.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping git object tests")
	}

	// Create a temporary directory and initialise a git repo.
	repoDir := t.TempDir()

	if _, err := RunGitCommand(repoDir, "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Configure user for the temp repo so commits work if needed.
	RunGitCommand(repoDir, "config", "user.email", "test@test.com")
	RunGitCommand(repoDir, "config", "user.name", "Test")

	// Write a small text file into the repo directory.
	testFile := filepath.Join(repoDir, "hello.txt")
	content := []byte("Hello, gitsize-guard!\nThis is a test file.\n")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// ObjectAlreadyExists should return false — the blob hasn't been written yet.
	t.Run("object does not exist before write", func(t *testing.T) {
		exists, err := ObjectAlreadyExists(repoDir, testFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if exists {
			t.Error("expected object to NOT exist before hash-object -w, but it does")
		}
	})

	// EstimateCompressedSize should return a positive number.
	var compressedSize int64
	t.Run("estimate compressed size is positive", func(t *testing.T) {
		size, err := EstimateCompressedSize(repoDir, testFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if size <= 0 {
			t.Errorf("expected positive compressed size, got %d", size)
		}
		compressedSize = size
		t.Logf("file size on disk: %d bytes, git object size: %d bytes", len(content), compressedSize)
	})

	// ObjectAlreadyExists should now return true — EstimateCompressedSize wrote the blob.
	t.Run("object exists after write", func(t *testing.T) {
		exists, err := ObjectAlreadyExists(repoDir, testFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !exists {
			t.Error("expected object to exist after hash-object -w, but it does not")
		}
	})
}

func TestRunGitCommand(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping")
	}

	t.Run("valid command returns output", func(t *testing.T) {
		out, err := RunGitCommand(".", "version")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out == "" {
			t.Error("expected non-empty output from git version")
		}
		t.Logf("git version: %s", out)
	})

	t.Run("invalid command returns error with stderr", func(t *testing.T) {
		_, err := RunGitCommand(".", "not-a-real-command")
		if err == nil {
			t.Error("expected error for invalid git command, got nil")
		}
	})
}
