package analyzer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ratuhin1122/gitsize-guard/internal/similarity"
)

func gitFileChecksums(t *testing.T, repo string) map[string][sha256.Size]byte {
	t.Helper()
	root := filepath.Join(repo, ".git")
	checksums := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, file)
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		var digest [sha256.Size]byte
		copy(digest[:], hash.Sum(nil))
		checksums[rel] = digest
		return nil
	})
	if err != nil {
		t.Fatalf("checksum .git: %v", err)
	}
	return checksums
}

func assertGitFilesUnchanged(t *testing.T, repo string) func() {
	t.Helper()
	before := gitFileChecksums(t, repo)
	return func() {
		t.Helper()
		after := gitFileChecksums(t, repo)
		if !reflect.DeepEqual(before, after) {
			for path, digest := range before {
				if got, ok := after[path]; !ok || got != digest {
					t.Errorf("analysis changed or removed .git file %q", path)
				}
			}
			for path := range after {
				if _, ok := before[path]; !ok {
					t.Errorf("analysis created .git file %q", path)
				}
			}
		} else {
			t.Logf("git_unchanged=true files=%d (SHA-256 of every .git file)", len(before))
		}
	}
}

func TestNearDupReadOnlyIntegration(t *testing.T) {
	// newRepo skips the integration test when Git is not available on PATH.
	repo := newRepo(t)
	cfg := DefaultNearDupConfig()
	cfg.TimeBudget = time.Minute
	seededBytes := func(seed int64, size int) []byte {
		data := make([]byte, size)
		if _, err := rand.New(rand.NewSource(seed)).Read(data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	original := seededBytes(2026, 12*mib)
	writeFile(t, filepath.Join(repo, "original.bin"), original)
	git(t, repo, "add", "original.bin")
	git(t, repo, "commit", "-qm", "deterministic original")

	modified := bytes.Clone(original)
	middle, changedBytes := len(modified)/2, len(modified)/20
	// Keep the 5% edit contiguous: scattered edits can invalidate every chunk.
	start := middle - changedBytes/2
	for i := start; i < start+changedBytes; i++ {
		modified[i] ^= 0xff
	}
	withInsertion := make([]byte, 0, len(modified)+200)
	withInsertion = append(withInsertion, modified[:middle]...)
	withInsertion = append(withInsertion, seededBytes(7, 200)...)
	withInsertion = append(withInsertion, modified[middle:]...)
	writeFile(t, filepath.Join(repo, "modified-copy.bin"), withInsertion)
	writeFile(t, filepath.Join(repo, "unrelated.bin"), seededBytes(99, 12*mib))
	writeFile(t, filepath.Join(repo, "small-copy.bin"), original[:mib])

	// Snapshot every .git file, including index, config, refs, logs and objects.
	// No setup Git commands run between this snapshot and the final assertion.
	defer assertGitFilesUnchanged(t, repo)()
	for _, tc := range []struct {
		name, path    string
		wantDuplicate bool
		size          int64
	}{
		{"five percent edit plus insertion", "modified-copy.bin", true, int64(len(withInsertion))},
		{"unrelated random file", "unrelated.bin", false, 12 * mib},
		{"one MiB copy below minimum", "small-copy.bin", false, mib},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer assertGitFilesUnchanged(t, repo)()
			report, err := AnalyzeWithNearDup([]Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{tc.path}}}, DefaultThresholds(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Files) != 1 || report.Files[0].SizeBytes != tc.size || report.GrowthBytes != tc.size {
				t.Fatalf("unexpected size/growth: %+v", report)
			}
			if len(report.Notes) != 0 {
				t.Fatalf("unexpected skipped candidates: %v", report.Notes)
			}
			dup := report.Files[0].NearDuplicate
			t.Logf("file=%s size=%d growth_bytes=%d near_duplicates=%d", tc.path, tc.size, report.GrowthBytes, report.NearDuplicates)
			if !tc.wantDuplicate {
				if dup != nil || report.NearDuplicates != 0 || report.RiskLevel != RiskLow {
					t.Fatalf("unexpected near duplicate: %+v", report)
				}
				return
			}
			if dup == nil || dup.Path != "original.bin" || dup.Similarity < .90 || dup.CandidateSize != int64(len(original)) || report.NearDuplicates != 1 {
				t.Fatalf("missing near duplicate or similarity below 0.90: %+v (notes %v)", dup, report.Notes)
			}
			t.Logf("near_duplicate path=%s similarity=%.6f shared_bytes=%d candidate_size=%d", dup.Path, dup.Similarity, dup.SharedBytes, dup.CandidateSize)
		})
	}

	t.Run("candidate read exceeds shared budget", func(t *testing.T) {
		defer assertGitFilesUnchanged(t, repo)()
		file := FileImpact{FilePath: "modified-copy.bin", SizeBytes: int64(len(withInsertion)), GrowthBytes: int64(len(withInsertion)), ChangeType: ChangeFile}
		// Inject a smaller allowance into the production check to cross the
		// budget without allocating/reading a 512 MiB fixture. The new content
		// fits, but reading the HEAD candidate would exceed the remaining bytes.
		check, cancel := newNearDupCheck(cfg.TimeBudget)
		defer cancel()
		check.remaining = int64(len(withInsertion) + len(original) - 1)
		var readErr error
		notes := check.annotate(repo, &file, cfg, func() ([]similarity.ChunkRef, error) {
			var chunks []similarity.ChunkRef
			chunks, readErr = similarity.Chunk(nearDupReader{check: check, reader: bytes.NewReader(withInsertion)})
			return chunks, readErr
		})
		if readErr != nil {
			t.Fatalf("new-file read failed: %v", readErr)
		}
		if err := check.ctx.Err(); err != nil && err != context.Canceled {
			t.Fatalf("check exhausted time rather than bytes: %v", err)
		}
		report := NewReport([]FileImpact{file}, DefaultThresholds())
		report.Notes = notes
		if len(notes) != 1 || !strings.Contains(notes[0], "byte budget") || file.NearDuplicate != nil || report.NearDuplicates != 0 || report.GrowthBytes != int64(len(withInsertion)) {
			t.Fatalf("budget skip must be advisory, with unchanged growth: %+v", report)
		}
		t.Logf("budget_note=%q error=%v growth_bytes=%d", notes[0], readErr, report.GrowthBytes)
	})
}
