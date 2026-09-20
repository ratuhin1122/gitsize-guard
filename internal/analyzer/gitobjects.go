package analyzer

import (
	"fmt"
	"os/exec"
	"strings"
)

// RunGitCommand runs `git <args...>` with Dir set to repoPath.
// Returns trimmed stdout, or an error including stderr content on failure.
func RunGitCommand(repoPath string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoPath

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s failed: %w\nstderr: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return strings.TrimSpace(stdout.String()), nil
}

// ObjectAlreadyExists checks whether the exact content of filePath already
// exists as a blob in the Git object store at repoPath.
//
// It computes the blob hash via `git hash-object` (without writing), then
// checks existence with `git cat-file -e`. Returns true if the object
// already exists (meaning this exact content costs ~0 new bytes to commit).
func ObjectAlreadyExists(repoPath string, filePath string) (bool, error) {
	hash, err := RunGitCommand(repoPath, "hash-object", filePath)
	if err != nil {
		return false, fmt.Errorf("hash-object failed: %w", err)
	}

	// git cat-file -e exits non-zero if the object doesn't exist.
	// We treat that as "does not exist", not as a Go error.
	cmd := exec.Command("git", "cat-file", "-e", hash)
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		return false, nil
	}

	return true, nil
}

// EstimateCompressedSize writes the file's content as a blob into the Git
// object store and returns the object's size as reported by Git.
//
// SIDE EFFECT: This actually writes a new blob object into .git/objects.
// The blob is loose and unreferenced, so `git gc` or `git prune` will
// eventually clean it up if it's never committed.
func EstimateCompressedSize(repoPath string, filePath string) (int64, error) {
	// -w flag writes the blob into the object store
	hash, err := RunGitCommand(repoPath, "hash-object", "-w", filePath)
	if err != nil {
		return 0, fmt.Errorf("hash-object -w failed: %w", err)
	}

	sizeStr, err := RunGitCommand(repoPath, "cat-file", "-s", hash)
	if err != nil {
		return 0, fmt.Errorf("cat-file -s failed: %w", err)
	}

	var size int64
	if _, err := fmt.Sscanf(sizeStr, "%d", &size); err != nil {
		return 0, fmt.Errorf("failed to parse object size %q: %w", sizeStr, err)
	}

	return size, nil
}
