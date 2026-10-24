package analyzer

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGeneratedTrackedPrefixes(t *testing.T) {
	for _, tracked := range []bool{false, true} {
		name := "dist not tracked"
		if tracked {
			name = "dist tracked in HEAD"
		}
		t.Run(name, func(t *testing.T) {
			repo := newRepo(t)
			file := filepath.Join("dist", "nested", "app.js")
			writeFile(t, filepath.Join(repo, file), []byte("content"))
			if tracked {
				git(t, repo, "add", "--", file)
				git(t, repo, "commit", "-qm", "dist")
			} else {
				// Being staged alone is not evidence of intentional HEAD content.
				git(t, repo, "add", "--", file)
			}
			defer assertGitFilesUnchanged(t, repo)()
			prefixes, err := trackedPrefixes(repo)
			if err != nil {
				t.Fatal(err)
			}
			if prefixes["dist"] != tracked || prefixes[filepath.ToSlash(filepath.Dir(file))] != tracked {
				t.Fatalf("prefixes = %v, want dist tracked=%t", prefixes, tracked)
			}
			if !prefixes["README.md"] {
				t.Fatalf("missing first segment of root file: %v", prefixes)
			}
		})
	}
}

func TestGeneratedIgnoredByGitignore(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), []byte("dist/\n!dist/keep.txt\n"))
	ignoredPath := filepath.Join("dist", "app.js")
	writeFile(t, filepath.Join(repo, ignoredPath), []byte("content"))
	git(t, repo, "add", "-f", "--", ignoredPath)
	defer assertGitFilesUnchanged(t, repo)()
	ignored, err := ignoredByGitignore(repo, []string{ignoredPath, "README.md", "space name.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !ignored[filepath.ToSlash(ignoredPath)] || len(ignored) != 1 {
		t.Fatalf("ignored = %v", ignored)
	}
	ignored, err = ignoredByGitignore(repo, []string{"README.md"})
	if err != nil || len(ignored) != 0 {
		t.Fatalf("no matches = %v, %v", ignored, err)
	}
	ignored, err = ignoredByGitignore(repo, nil)
	if err != nil || len(ignored) != 0 {
		t.Fatalf("empty paths = %v, %v", ignored, err)
	}
}

func TestGeneratedNoHEAD(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	defer assertGitFilesUnchanged(t, repo)()
	prefixes, err := trackedPrefixes(repo)
	if err != nil || len(prefixes) != 0 {
		t.Fatalf("fresh repo = %v, %v", prefixes, err)
	}
}

func TestGeneratedPathCap(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), []byte("*.log\n"))
	writeFile(t, filepath.Join(repo, "dist", "app.js"), []byte("content"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "fixtures")
	defer assertGitFilesUnchanged(t, repo)()
	t.Run("ignored candidates", func(t *testing.T) {
		check, cancel := newGeneratedCheck(time.Minute, 1)
		defer cancel()
		ignored, err := check.ignoredByGitignore(repo, []string{"first.log", "second.log"})
		if err != nil || !ignored["first.log"] || ignored["second.log"] {
			t.Fatalf("capped result = %v, %v", ignored, err)
		}
		if len(check.notes) != 1 || !strings.Contains(check.notes[0], "limited to 1 paths") {
			t.Fatalf("notes = %v", check.notes)
		}
	})
	t.Run("HEAD paths", func(t *testing.T) {
		check, cancel := newGeneratedCheck(time.Minute, 1)
		defer cancel()
		prefixes, err := check.trackedPrefixes(repo)
		if err != nil || len(prefixes) != 1 || len(check.notes) != 1 {
			t.Fatalf("capped prefixes = %v, notes=%v, err=%v", prefixes, check.notes, err)
		}
	})
}

func TestGeneratedTimeBudget(t *testing.T) {
	repo := newRepo(t)
	defer assertGitFilesUnchanged(t, repo)()
	check, cancel := newGeneratedCheck(time.Minute, 0)
	cancel() // Deterministically exhaust the budget without sleeping.
	_, err := check.trackedPrefixes(repo)
	if err != nil || len(check.notes) != 1 || !strings.Contains(check.notes[0], "time budget") {
		t.Fatalf("notes=%v err=%v", check.notes, err)
	}
	_, err = check.ignoredByGitignore(repo, []string{"file.log"})
	if err != nil || len(check.notes) != 2 {
		t.Fatalf("notes=%v err=%v", check.notes, err)
	}
}

func TestGeneratedDefaultPathCap(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), []byte("*.log\n"))
	defer assertGitFilesUnchanged(t, repo)()
	check, cancel := newGeneratedCheck(0, 0)
	defer cancel()
	if check.maxPaths != 5000 {
		t.Fatalf("default cap = %d", check.maxPaths)
	}
	deadline, ok := check.ctx.Deadline()
	if !ok || time.Until(deadline) > 5*time.Second {
		t.Fatalf("default deadline = %v", deadline)
	}
	paths := make([]string, 5001)
	for i := range paths {
		paths[i] = "file.log"
	}
	_, err := check.ignoredByGitignore(repo, paths)
	if err != nil || len(check.notes) != 1 {
		t.Fatalf("default cap notes=%v err=%v", check.notes, err)
	}
}
