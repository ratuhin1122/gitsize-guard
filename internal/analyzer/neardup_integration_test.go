package analyzer

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ratuhin1122/gitsize-guard/internal/similarity"
)

func nearDupRiskFixture(t *testing.T) (string, []byte) {
	t.Helper()
	repo := newRepo(t)
	// Registered after TempDir's cleanup, so this runs while the fixture is
	// still owned by this subtest, before Go is supposed to remove it.
	t.Cleanup(func() {
		if _, err := os.Stat(repo); err != nil {
			t.Errorf("risk fixture disappeared before its owning subtest completed: %v", err)
		}
	})
	data := make([]byte, 2*mib)
	if _, err := rand.New(rand.NewSource(2026)).Read(data); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "existing.bin"), data)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "existing")
	modified := bytes.Clone(data)
	chunks, err := similarity.Chunk(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	offset, edited := 0, false
	for _, chunk := range chunks {
		if chunk.Size <= int64(len(data)/10) {
			// Edit before the minimum boundary window; the rolling hash recovers
			// before the first eligible boundary, changing only this chunk's hash.
			for i := offset + 100; i < offset+200; i++ {
				modified[i] ^= 0xff
			}
			edited = true
			break
		}
		offset += int(chunk.Size)
	}
	if !edited {
		t.Fatal("deterministic fixture has no chunk below 10% of its size")
	}
	writeFile(t, filepath.Join(repo, "copy.bin"), modified)
	return repo, modified
}

func TestNearDupAnalysisRiskAndGrowth(t *testing.T) {
	cfg := DefaultNearDupConfig()
	cfg.MinBytes = mib
	cfg.TimeBudget = time.Minute
	for _, tc := range []struct {
		name       string
		thresholds Thresholds
		want       string
	}{
		{"low becomes medium", Thresholds{10 * mib, 20 * mib}, RiskMedium},
		{"medium stays medium", Thresholds{mib, 20 * mib}, RiskMedium},
		{"high stays high", Thresholds{mib, 2 * mib}, RiskHigh},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case owns its TempDir; no case depends on a sibling's fixture
			// or on the later staged-content mutation.
			repo, modified := nearDupRiskFixture(t)
			sel := []Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{"copy.bin"}}}
			disabled := cfg
			disabled.Enabled = false
			baseline, err := AnalyzeWithNearDup(sel, tc.thresholds, disabled)
			if err != nil {
				t.Fatal(err)
			}
			before := objectState(t, repo)
			got, err := AnalyzeWithNearDup(sel, tc.thresholds, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got.RiskLevel != tc.want || got.NearDuplicates != 1 {
				t.Fatalf("risk/count = %s/%d; notes=%v", got.RiskLevel, got.NearDuplicates, got.Notes)
			}
			if got.GrowthBytes != baseline.GrowthBytes || got.Files[0].GrowthBytes != baseline.Files[0].GrowthBytes || got.Files[0].SizeBytes != baseline.Files[0].SizeBytes {
				t.Fatal("advisory changed exact size or growth")
			}
			dup := got.Files[0].NearDuplicate
			if dup == nil || dup.Path != "existing.bin" || dup.Similarity < .9 || dup.CandidateSize != int64(len(modified)) || dup.SharedBytes <= 0 {
				t.Fatalf("near duplicate = %+v", dup)
			}
			if !strings.Contains(strings.Join(got.Reasons, " "), "already in HEAD") || !strings.Contains(got.Recommendation, "Replace the existing file") {
				t.Fatalf("missing advisory: %+v", got)
			}
			if objectState(t, repo) != before {
				t.Fatal("advisory wrote repository objects")
			}
			encoded, err := got.ToJSON()
			if err != nil {
				t.Fatal(err)
			}
			var report map[string]any
			if err := json.Unmarshal([]byte(encoded), &report); err != nil {
				t.Fatal(err)
			}
			if report["near_duplicates"] != float64(1) {
				t.Fatal("JSON count missing")
			}
			file := report["files"].([]any)[0].(map[string]any)
			if file["near_duplicate"].(map[string]any)["path"] != "existing.bin" {
				t.Fatal("JSON advisory missing")
			}
			if baseline.NearDuplicates != 0 || baseline.Files[0].NearDuplicate != nil {
				t.Fatal("disabled detection returned advisory")
			}
		})
	}
	t.Run("staged content", func(t *testing.T) {
		// Staged content, rather than a subsequent working-tree edit, is analyzed.
		repo, modified := nearDupRiskFixture(t)
		git(t, repo, "add", "copy.bin")
		writeFile(t, filepath.Join(repo, "copy.bin"), []byte("different unstaged content"))
		staged, err := AnalyzeWithNearDup([]Selection{{Dir: repo, Kind: KindStaged}}, DefaultThresholds(), cfg)
		if err != nil || staged.NearDuplicates != 1 || staged.GrowthBytes != int64(len(modified)) {
			t.Fatalf("staged: %+v, %v", staged, err)
		}
	})
}

func TestNearDupAnalysisNoHEADAndMinimum(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "checkout", "--orphan", "fresh")
	writeFile(t, filepath.Join(repo, "new.bin"), bytes.Repeat([]byte{'x'}, 5*mib))
	got, err := Analyze([]Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{"new.bin"}}}, DefaultThresholds())
	if err != nil || got.RiskLevel != RiskLow || got.NearDuplicates != 0 || len(got.Notes) != 0 {
		t.Fatalf("fresh: %+v, %v", got, err)
	}
	// Disabled/below-minimum checks do not even need a valid repository/reader.
	for _, cfg := range []NearDupConfig{{Enabled: false}, DefaultNearDupConfig()} {
		file := FileImpact{FilePath: "small", SizeBytes: 1, GrowthBytes: 1}
		if notes := CheckNearDuplicate(filepath.Join(repo, "missing"), &file, nil, cfg); len(notes) != 0 || file.NearDuplicate != nil {
			t.Fatalf("ineligible file checked: %v", notes)
		}
	}
}

func TestNearDupReportCountsBeforeTruncationAndNotesJSON(t *testing.T) {
	files := make([]FileImpact, maxReportedFiles+1)
	for i := range files {
		files[i] = FileImpact{FilePath: "copy", NearDuplicate: &NearDuplicate{Path: "original", Similarity: 1}}
	}
	report := NewReport(files, DefaultThresholds())
	if report.NearDuplicates != len(files) || report.RiskLevel != RiskMedium || report.GrowthBytes != 0 {
		t.Fatalf("report = %+v", report)
	}
	report.Notes = []string{"Skipped candidate: byte budget reached."}
	encoded, err := report.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, `"notes"`) {
		t.Fatal("missing notes JSON")
	}
	empty, err := NewReport(nil, DefaultThresholds()).ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty, `"near_duplicate`) || strings.Contains(empty, `"notes"`) {
		t.Fatal("empty optional fields were not omitted")
	}
}

func TestNearDupNotesReachReport(t *testing.T) {
	repo := newRepo(t)
	data := randomBytes(t, 5*mib)
	writeFile(t, filepath.Join(repo, "old.bin"), data)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "old")
	oid := git(t, repo, "rev-parse", "HEAD:old.bin")
	object := filepath.Join(repo, ".git", "objects", oid[:2], oid[2:])
	if err := os.Remove(object); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(repo, "new.bin"), data)
	got, err := Analyze([]Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{"new.bin"}}}, DefaultThresholds())
	if err != nil || len(got.Notes) == 0 || got.RiskLevel != RiskLow {
		t.Fatalf("missing object: %+v, %v", got, err)
	}
}
