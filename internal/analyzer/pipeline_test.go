package analyzer

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const mib = 1024 * 1024

// testThresholds keep test files small: warn at 1 MiB, fail at 2 MiB.
var testThresholds = Thresholds{WarningBytes: 1 * mib, FailureBytes: 2 * mib}

// newRepo creates a git repository with one committed file.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping integration test")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(dir, "README.md"), []byte("# test\n"))
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := RunGitCommand(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// randomBytes returns incompressible, NUL-containing (binary) content.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[0] = 0
	return b
}

// objectState summarises the object store so tests can assert it is untouched.
func objectState(t *testing.T, dir string) string {
	t.Helper()
	return git(t, dir, "count-objects", "-v")
}

func analyze(t *testing.T, sels ...Selection) *Report {
	t.Helper()
	r, err := Analyze(sels, testThresholds)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	return r
}

func growthOf(r *Report, path string) (int64, bool) {
	for _, f := range r.Files {
		if f.FilePath == path {
			return f.GrowthBytes, true
		}
	}
	return 0, false
}

func TestAnalyzeDoesNotWriteObjects(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "model.bin"), randomBytes(t, 3*mib))
	before := objectState(t, repo)

	sel := Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"model.bin"}}
	first := analyze(t, sel)
	second := analyze(t, sel) // a retry must not be judged differently

	if first.RiskLevel != RiskHigh || second.RiskLevel != RiskHigh {
		t.Fatalf("risk = %s then %s, want high both times", first.RiskLevel, second.RiskLevel)
	}
	if first.GrowthBytes != 3*mib || second.GrowthBytes != 3*mib {
		t.Errorf("growth = %d then %d, want %d", first.GrowthBytes, second.GrowthBytes, 3*mib)
	}
	if after := objectState(t, repo); after != before {
		t.Errorf("object store changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if !first.LargestFile.IsBinary || first.LargestFile.ChangeType != ChangeAdded {
		t.Errorf("largest = %+v, want binary added file", first.LargestFile)
	}
	if !strings.Contains(first.Recommendation, "Git LFS") {
		t.Errorf("recommendation = %q, want Git LFS advice", first.Recommendation)
	}
}

func TestAnalyzePathspecs(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), []byte("ignored/\n"))
	writeFile(t, filepath.Join(repo, "sub", "a.bin"), randomBytes(t, 1500))
	writeFile(t, filepath.Join(repo, "sub", "deep", "b.bin"), randomBytes(t, 2500))
	writeFile(t, filepath.Join(repo, "top.bin"), randomBytes(t, 4000))
	writeFile(t, filepath.Join(repo, "ignored", "c.bin"), randomBytes(t, 8000))
	writeFile(t, filepath.Join(repo, "README.md"), []byte("# changed\n"))
	sub := filepath.Join(repo, "sub")

	tests := []struct {
		name string
		sel  Selection
		want map[string]int64
	}{
		{"dot in subdirectory", Selection{Dir: sub, Kind: KindPaths, Pathspecs: []string{"."}},
			map[string]int64{"sub/a.bin": 1500, "sub/deep/b.bin": 2500}},
		{"relative to Dir", Selection{Dir: sub, Kind: KindPaths, Pathspecs: []string{"a.bin"}},
			map[string]int64{"sub/a.bin": 1500}},
		{"directory", Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"sub"}},
			map[string]int64{"sub/a.bin": 1500, "sub/deep/b.bin": 2500}},
		{"glob", Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"*.bin"}},
			map[string]int64{"sub/a.bin": 1500, "sub/deep/b.bin": 2500, "top.bin": 4000}},
		{"whole tree skips ignored", Selection{Dir: sub, Kind: KindPaths, Pathspecs: []string{":/"}},
			map[string]int64{"sub/a.bin": 1500, "sub/deep/b.bin": 2500, "top.bin": 4000, "README.md": 10, ".gitignore": 9}},
		{"force includes ignored", Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"ignored"}, Force: true},
			map[string]int64{"ignored/c.bin": 8000}},
		{"ignored without force", Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"ignored"}},
			map[string]int64{}},
		{"tracked only", Selection{Dir: repo, Kind: KindTracked, Pathspecs: []string{":/"}},
			map[string]int64{"README.md": 10}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := analyze(t, tc.sel)
			got := map[string]int64{}
			for _, f := range r.Files {
				got[f.FilePath] = f.GrowthBytes
			}
			if len(got) != len(tc.want) {
				t.Fatalf("files = %v, want %v", got, tc.want)
			}
			for p, n := range tc.want {
				if got[p] != n {
					t.Errorf("growth[%s] = %d, want %d (all: %v)", p, got[p], n, got)
				}
			}
		})
	}
}

func TestAnalyzeStaged(t *testing.T) {
	for _, unborn := range []bool{false, true} {
		name := "with HEAD"
		if unborn {
			name = "unborn HEAD"
		}
		t.Run(name, func(t *testing.T) {
			var repo string
			if unborn {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("git not found on PATH")
				}
				repo = t.TempDir()
				git(t, repo, "init", "-q")
			} else {
				repo = newRepo(t)
			}
			writeFile(t, filepath.Join(repo, "model.bin"), randomBytes(t, 3*mib))
			git(t, repo, "add", "model.bin")

			r := analyze(t, Selection{Dir: repo, Kind: KindStaged})
			if r.RiskLevel != RiskHigh || r.GrowthBytes != 3*mib {
				t.Fatalf("risk=%s growth=%d, want high %d", r.RiskLevel, r.GrowthBytes, 3*mib)
			}
			if r.LargestFile.ChangeType != ChangeStaged {
				t.Errorf("change type = %q, want staged", r.LargestFile.ChangeType)
			}
		})
	}
}

func TestAnalyzeWorkingTreeSupersedesStaged(t *testing.T) {
	repo := newRepo(t)
	path := filepath.Join(repo, "data.bin")
	writeFile(t, path, randomBytes(t, 3*mib))
	git(t, repo, "add", "data.bin")
	writeFile(t, path, randomBytes(t, 1000)) // shrunk after staging

	r := analyze(t,
		Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"data.bin"}},
		Selection{Dir: repo, Kind: KindStaged},
	)
	if r.FilesAnalyzed != 1 || r.GrowthBytes != 1000 {
		t.Errorf("files=%d growth=%d, want 1 file of 1000 bytes (the content git add would stage)", r.FilesAnalyzed, r.GrowthBytes)
	}
}

func TestAnalyzeDedupesAgainstHEADOnly(t *testing.T) {
	repo := newRepo(t)
	content := randomBytes(t, 3*mib)
	writeFile(t, filepath.Join(repo, "committed.bin"), content)
	git(t, repo, "add", "committed.bin")
	git(t, repo, "commit", "-q", "-m", "add committed.bin")

	// An identical copy of committed content adds nothing.
	writeFile(t, filepath.Join(repo, "copy.bin"), content)
	r := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"copy.bin"}})
	if g, _ := growthOf(r, "copy.bin"); g != 0 || r.RiskLevel != RiskLow {
		t.Errorf("copy of committed content: growth=%d risk=%s, want 0 low", g, r.RiskLevel)
	}

	// A blob that is merely present as a loose object (e.g. from an earlier,
	// unstaged `git add`) is not in history and still counts.
	loose := filepath.Join(repo, "loose.bin")
	writeFile(t, loose, randomBytes(t, 3*mib))
	git(t, repo, "hash-object", "-w", "loose.bin")
	r = analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"loose.bin"}})
	if g, _ := growthOf(r, "loose.bin"); g != 3*mib || r.RiskLevel != RiskHigh {
		t.Errorf("loose-only blob: growth=%d risk=%s, want %d high", g, r.RiskLevel, 3*mib)
	}
}

func TestAnalyzeFilteredFiles(t *testing.T) {
	repo := newRepo(t)
	// A clean filter that, like Git LFS, stores a small pointer instead of the content.
	git(t, repo, "config", "filter.pointer.clean", "cat >/dev/null; printf pointer-file")
	git(t, repo, "config", "filter.pointer.smudge", "cat")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.dat filter=pointer\n"))
	writeFile(t, filepath.Join(repo, "big.dat"), randomBytes(t, 3*mib))
	before := objectState(t, repo)

	r := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"big.dat"}})
	g, ok := growthOf(r, "big.dat")
	if !ok || g != int64(len("pointer-file")) || r.RiskLevel != RiskLow {
		t.Errorf("filtered file: growth=%d risk=%s, want %d low", g, r.RiskLevel, len("pointer-file"))
	}
	if after := objectState(t, repo); after != before {
		t.Errorf("object store changed while measuring a filtered file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestAnalyzePending(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "staged.bin"), randomBytes(t, 1000))
	git(t, repo, "add", "staged.bin")
	writeFile(t, filepath.Join(repo, "README.md"), []byte("# changed\n"))
	writeFile(t, filepath.Join(repo, "untracked.bin"), randomBytes(t, 2000))

	r := analyze(t, Selection{Dir: repo, Kind: KindPending})
	for _, p := range []string{"staged.bin", "README.md", "untracked.bin"} {
		if _, ok := growthOf(r, p); !ok {
			t.Errorf("pending selection is missing %s (files: %+v)", p, r.Files)
		}
	}
}

func TestAnalyzeFiles(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "x.txt"), []byte("hello"))

	r := analyze(t, Selection{Dir: repo, Kind: KindFiles, Pathspecs: []string{"x.txt", filepath.Join(repo, "README.md")}})
	if g, _ := growthOf(r, "x.txt"); g != 5 {
		t.Errorf("x.txt growth = %d, want 5", g)
	}
	if _, ok := growthOf(r, "README.md"); !ok {
		t.Errorf("README.md missing from %+v", r.Files)
	}

	if _, err := Analyze([]Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{"nope.txt"}}}, testThresholds); err == nil {
		t.Error("expected an error for a missing file")
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, []byte("x"))
	if _, err := Analyze([]Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{outside}}}, testThresholds); err == nil {
		t.Error("expected an error for a file outside the repository")
	}
}

func TestAnalyzeOutsideRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
	r := analyze(t, Selection{Dir: t.TempDir(), Kind: KindPending})
	if r.RiskLevel != RiskLow || r.FilesAnalyzed != 0 {
		t.Errorf("outside a repository: %+v, want an empty low-risk report", r)
	}
}

func TestNewReportCapsFiles(t *testing.T) {
	var files []FileImpact
	for i := 0; i < maxReportedFiles+5; i++ {
		files = append(files, FileImpact{FilePath: "f", GrowthBytes: int64(i)})
	}
	r := NewReport(files, testThresholds)
	if len(r.Files) != maxReportedFiles || r.FilesAnalyzed != maxReportedFiles+5 {
		t.Errorf("files=%d analyzed=%d", len(r.Files), r.FilesAnalyzed)
	}
	if r.Files[0].GrowthBytes != int64(maxReportedFiles+4) || r.LargestFile.GrowthBytes != r.Files[0].GrowthBytes {
		t.Errorf("files not sorted largest first: %+v", r.Files[:2])
	}
}

func TestAnalyzeLFSUsesPointerSize(t *testing.T) {
	repo := newRepo(t)
	// A stand-in for git-lfs that would leave a marker if it were ever run.
	marker := filepath.Join(t.TempDir(), "filter-ran")
	git(t, repo, "config", "filter.lfs.clean", "touch "+marker+"; cat")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.psd filter=lfs\n*.raw filter=unconfigured\n"))
	for i := 0; i < 50; i++ { // many small LFS files: each still counts as a pointer
		writeFile(t, filepath.Join(repo, "art", "img"+string(rune('a'+i%26))+string(rune('a'+i/26))+".psd"), randomBytes(t, 40*1024))
	}
	writeFile(t, filepath.Join(repo, "big.psd"), randomBytes(t, 3*mib))
	writeFile(t, filepath.Join(repo, "plain.raw"), randomBytes(t, 2000)) // filter without a driver: stored as is
	before := objectState(t, repo)

	r := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"."}})
	if g, _ := growthOf(r, "big.psd"); g != lfsPointerSize(3*mib) {
		t.Errorf("big.psd growth = %d, want pointer size %d", g, lfsPointerSize(3*mib))
	}
	if g, _ := growthOf(r, "plain.raw"); g != 2000 {
		t.Errorf("plain.raw growth = %d, want 2000 (no filter driver configured)", g)
	}
	if r.RiskLevel != RiskLow {
		t.Errorf("risk = %s for LFS-tracked files, want low (%+v)", r.RiskLevel, r.Files[:3])
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the LFS clean filter ran; measuring must not copy files into .git/lfs")
	}
	if after := objectState(t, repo); after != before {
		t.Errorf("object store changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestAnalyzeWorkingTreeEncoding(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.txt working-tree-encoding=UTF-16LE\n"))
	utf16 := make([]byte, 0, 2*mib)
	for len(utf16) < 2*mib {
		utf16 = append(utf16, 'a', 0)
	}
	writeFile(t, filepath.Join(repo, "wide.txt"), utf16)

	r := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"wide.txt"}})
	if g, _ := growthOf(r, "wide.txt"); g != mib {
		t.Errorf("growth = %d, want %d (git stores the UTF-8 re-encoding)", g, mib)
	}
}

func TestAnalyzeStagedRenameAddsNothing(t *testing.T) {
	repo := newRepo(t)
	for i := 0; i < 20; i++ {
		writeFile(t, filepath.Join(repo, "assets", string(rune('a'+i))+".bin"), randomBytes(t, 100*1024))
	}
	git(t, repo, "add", "assets")
	git(t, repo, "commit", "-q", "-m", "assets")
	git(t, repo, "mv", "assets", "static")

	r := analyze(t, Selection{Dir: repo, Kind: KindStaged})
	if r.GrowthBytes != 0 || r.RiskLevel != RiskLow {
		t.Errorf("rename of committed files: growth=%d risk=%s, want 0 low", r.GrowthBytes, r.RiskLevel)
	}
}

func TestAnalyzeTrickyPaths(t *testing.T) {
	repo := newRepo(t)
	base := randomBytes(t, 3*mib)
	writeFile(t, filepath.Join(repo, "base.bin"), base)
	git(t, repo, "add", "base.bin")
	git(t, repo, "commit", "-q", "-m", "base")
	// Names that git hash-object --stdin-paths would unquote or trim, and
	// which must not be mistaken for the committed base.bin.
	for _, name := range []string{`"base.bin"`, "base.bin\r", "new\nline.bin"} {
		writeFile(t, filepath.Join(repo, name), randomBytes(t, 3*mib))
	}
	r := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{":/"}})
	if r.GrowthBytes != 9*mib {
		t.Errorf("growth = %d, want %d (three new files): %+v", r.GrowthBytes, 9*mib, r.Files)
	}
}

func TestAnalyzeRepoNameEndingInSpace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
	repo := filepath.Join(t.TempDir(), "repo ")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q")
	writeFile(t, filepath.Join(repo, "big.bin"), randomBytes(t, 3*mib))
	r := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"big.bin"}})
	if r.RiskLevel != RiskHigh {
		t.Errorf("risk = %s in a repo whose name ends in a space, want high", r.RiskLevel)
	}
}

func TestAnalyzePartialErrorsKeepTheRest(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "big.bin"), randomBytes(t, 3*mib))
	r, err := Analyze([]Selection{
		{Dir: repo, Kind: KindPaths, Pathspecs: []string{"big.bin"}},
		{Dir: repo, Kind: KindPaths, Pathspecs: []string{"/outside/the/repo"}},
	}, testThresholds)
	if err == nil {
		t.Error("expected an error for the pathspec outside the repository")
	}
	if r == nil || r.RiskLevel != RiskHigh {
		t.Errorf("report = %+v, want high risk from big.bin despite the other error", r)
	}
}

func TestAnalyzeRepositoriesSeparately(t *testing.T) {
	a, b := newRepo(t), newRepo(t)
	writeFile(t, filepath.Join(a, "one.bin"), randomBytes(t, 3*mib/2))
	writeFile(t, filepath.Join(b, "two.bin"), randomBytes(t, 3*mib/2))
	r := analyze(t,
		Selection{Dir: a, Kind: KindPaths, Pathspecs: []string{"one.bin"}},
		Selection{Dir: b, Kind: KindPaths, Pathspecs: []string{"two.bin"}},
	)
	if r.RiskLevel != RiskMedium || r.GrowthBytes != 3*mib/2 {
		t.Errorf("risk=%s growth=%d, want medium for the larger single repository", r.RiskLevel, r.GrowthBytes)
	}
	realA, _ := filepath.EvalSymlinks(a)
	realB, _ := filepath.EvalSymlinks(b)
	if !strings.HasPrefix(r.Files[0].FilePath, filepath.ToSlash(realA)) && !strings.HasPrefix(r.Files[0].FilePath, filepath.ToSlash(realB)) {
		t.Errorf("path %q should name its repository when several are involved", r.Files[0].FilePath)
	}
}

func TestAnalyzeFilesRejectsDirectories(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "assets", "a.bin"), randomBytes(t, 100))
	if _, err := Analyze([]Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{filepath.Join(repo, "assets")}}}, testThresholds); err == nil {
		t.Error("expected an error for a directory passed as a file")
	}
}

func TestAnalyzeListingDoesNotRunFilters(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "config", "filter.lfs.clean", "cat")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.psd filter=lfs\n"))
	writeFile(t, filepath.Join(repo, "art.psd"), randomBytes(t, 2*mib))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "art")

	marker := filepath.Join(t.TempDir(), "ran")
	git(t, repo, "config", "filter.lfs.clean", "touch "+marker+"; cat")
	git(t, repo, "config", "filter.lfs.required", "true")
	future := time.Now().Add(time.Hour) // make the index entry stat-dirty
	if err := os.Chtimes(filepath.Join(repo, "art.psd"), future, future); err != nil {
		t.Fatal(err)
	}
	analyze(t, Selection{Dir: repo, Kind: KindPending})
	if _, err := os.Stat(marker); err == nil {
		t.Error("listing candidates ran the LFS clean filter")
	}
}

func TestAnalyzeFilterRunsAreBounded(t *testing.T) {
	repo := newRepo(t)
	log := filepath.Join(t.TempDir(), "runs")
	git(t, repo, "config", "filter.count.clean", "echo run >> "+log+"; cat")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.dat filter=count\n"))
	for i := 0; i < 20; i++ { // small filtered files count at face value, without running the filter
		writeFile(t, filepath.Join(repo, "small", string(rune('a'+i))+".dat"), randomBytes(t, 1000))
	}
	for i := 0; i < maxStoreFiles+3; i++ {
		writeFile(t, filepath.Join(repo, "big", string(rune('A'+i%26))+string(rune('a'+i/26))+".dat"), randomBytes(t, mib))
	}
	r := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"."}})
	data, _ := os.ReadFile(log)
	if runs := strings.Count(string(data), "run"); runs > maxStoreFiles {
		t.Errorf("filter ran %d times, want at most %d", runs, maxStoreFiles)
	}
	if r.GrowthBytes < int64(maxStoreFiles+3)*mib {
		t.Errorf("growth = %d, want every file counted (at face value past the budget)", r.GrowthBytes)
	}
}

func TestAnalyzeRenormalize(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "config", "filter.pointer.clean", "cat >/dev/null; printf pointer-file")
	git(t, repo, "config", "filter.pointer.smudge", "cat")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.dat filter=pointer\n"))
	writeFile(t, filepath.Join(repo, "big.dat"), randomBytes(t, 3*mib))
	// Backdate the file so its index entry isn't "racily clean" (written in
	// the same second as the index), which would make git re-hash it anyway.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(repo, "big.dat"), past, past); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-q", "-m", "tracked through a filter")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("")) // like `git lfs untrack`

	plain := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"big.dat"}})
	if plain.GrowthBytes != 0 {
		t.Errorf("plain add of an unchanged tracked file: growth = %d, want 0", plain.GrowthBytes)
	}
	renorm := analyze(t, Selection{Dir: repo, Kind: KindPaths, Pathspecs: []string{"."}, Renormalize: true})
	if g, _ := growthOf(renorm, "big.dat"); g != 3*mib || renorm.RiskLevel != RiskHigh {
		t.Errorf("renormalize after dropping the filter: growth = %d risk = %s, want %d high", g, renorm.RiskLevel, 3*mib)
	}
}

func TestAnalyzeForcedPendingIncludesIgnored(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), []byte("*.bin\n"))
	writeFile(t, filepath.Join(repo, "model.bin"), randomBytes(t, 3*mib))
	if r := analyze(t, Selection{Dir: repo, Kind: KindPending}); r.RiskLevel != RiskLow {
		t.Errorf("pending without force: risk = %s, want low (file is ignored)", r.RiskLevel)
	}
	if r := analyze(t, Selection{Dir: repo, Kind: KindPending, Force: true}); r.RiskLevel != RiskHigh {
		t.Errorf("pending with force: risk = %s, want high", r.RiskLevel)
	}
}

func TestAnalyzeIdenticalStagedContentCountsOnce(t *testing.T) {
	repo := newRepo(t)
	content := randomBytes(t, 3*mib/2)
	writeFile(t, filepath.Join(repo, "a.bin"), content)
	writeFile(t, filepath.Join(repo, "b.bin"), content)
	git(t, repo, "add", "a.bin", "b.bin")
	r := analyze(t, Selection{Dir: repo, Kind: KindStaged})
	if r.GrowthBytes != 3*mib/2 {
		t.Errorf("growth = %d, want %d (git stores the blob once)", r.GrowthBytes, 3*mib/2)
	}
}

func TestWriteGrowth(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "config", "filter.lfs.clean", "cat")
	git(t, repo, "config", "filter.other.clean", "cat")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.psd filter=lfs\n*.enc filter=other\n"))
	for _, tc := range []struct {
		path  string
		want  int64
		exact bool
	}{
		{"art.psd", lfsPointerSize(3 * mib), true},
		{"secret.enc", 3 * mib, false},
		{"plain.txt", 3 * mib, true},
	} {
		got, exact, err := WriteGrowth(repo, tc.path, 3*mib)
		if err != nil || got != tc.want || exact != tc.exact {
			t.Errorf("WriteGrowth(%s) = %d, %v, %v; want %d, %v", tc.path, got, exact, err, tc.want, tc.exact)
		}
	}
}
