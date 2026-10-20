package analyzer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ratuhin1122/gitsize-guard/internal/similarity"
)

func TestNearDupDefaults(t *testing.T) {
	if got := DefaultNearDupConfig(); got != (NearDupConfig{true, 0.90, 5 << 20}) {
		t.Fatalf("defaults = %+v", got)
	}
}

func TestFindNearDupCandidates(t *testing.T) {
	repo := newRepo(t)
	for name, size := range map[string]int{
		"z.bin": 10000, "a.bin": 10000, "b.bin": 9999,
		"c.bin": 10001, "d.bin": 9000, "e.bin": 11000,
		"lower.bin": 5000, "upper.bin": 20000,
		"small.bin": 4999, "large.bin": 20001,
	} {
		writeFile(t, filepath.Join(repo, name), bytes.Repeat([]byte{'x'}, size))
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "candidates")
	before := objectState(t, repo)
	cfg := NearDupConfig{Enabled: true, Threshold: .9, MinBytes: 100}
	got, notes := findCandidates(repo, 10000, cfg)
	if len(notes) != 0 {
		t.Fatalf("notes: %v", notes)
	}
	var paths []string
	for _, cand := range got {
		paths = append(paths, cand.rel)
	}
	if want := []string{"a.bin", "z.bin", "b.bin", "c.bin", "d.bin"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	again, notes := findCandidates(repo, 10000, cfg)
	if len(notes) != 0 || !reflect.DeepEqual(got, again) {
		t.Fatal("discovery is not deterministic")
	}
	if after := objectState(t, repo); after != before {
		t.Fatal("discovery wrote Git objects")
	}
	if got, notes := findCandidates(repo, 10000, NearDupConfig{}); len(got) != 0 || len(notes) != 0 {
		t.Fatal("disabled discovery returned results")
	}
}

func TestFindNearDupBoundsAndLFSPointers(t *testing.T) {
	repo := newRepo(t)
	pointer := "version https://git-lfs.github.com/spec/v1\noid sha256:" + strings.Repeat("a", 64) + "\nsize 10000000\n"
	writeFile(t, filepath.Join(repo, "pointer.lfs"), []byte(pointer))
	for name, size := range map[string]int{"below": 49, "lower": 50, "same": 100, "upper": 200, "above": 201} {
		writeFile(t, filepath.Join(repo, name), bytes.Repeat([]byte{'x'}, size))
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "bounds")
	got, notes := findCandidates(repo, 100, NearDupConfig{Enabled: true, MinBytes: 50})
	if len(notes) != 0 {
		t.Fatal(notes)
	}
	var paths []string
	for _, cand := range got {
		paths = append(paths, cand.rel)
	}
	if want := []string{"same", "lower", "upper"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
}

func TestFindNearDupUnbornHEAD(t *testing.T) {
	repo := newRepo(t)
	git(t, repo, "checkout", "--orphan", "fresh")
	got, notes := findCandidates(repo, 100, DefaultNearDupConfig())
	if len(got) != 0 || len(notes) != 0 {
		t.Fatalf("unborn HEAD: %v, %v", got, notes)
	}
}

func TestChunkNearDupCandidateDoesNotWrite(t *testing.T) {
	repo := newRepo(t)
	data := randomBytes(t, 512<<10)
	path := filepath.Join("nested", "file with spaces.bin")
	writeFile(t, filepath.Join(repo, path), data)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "blob")
	oid := git(t, repo, "rev-parse", "HEAD:"+filepath.ToSlash(path))
	before := objectState(t, repo)
	got, err := chunkCandidate(repo, oid)
	if err != nil {
		t.Fatal(err)
	}
	want, err := similarity.Chunk(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("streamed blob chunks differ")
	}
	if objectState(t, repo) != before {
		t.Fatal("chunking wrote Git objects")
	}
}

func TestNearDupBudgetsAndMissingBlob(t *testing.T) {
	repo := newRepo(t)
	for name, size := range map[string]int{"a": 100, "b": 120} {
		writeFile(t, filepath.Join(repo, name), bytes.Repeat([]byte(name), size))
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "budget")
	a := candidate{rel: "a", blob: git(t, repo, "rev-parse", "HEAD:a"), size: 100}
	b := candidate{rel: "b", blob: git(t, repo, "rev-parse", "HEAD:b"), size: 120}
	check := &nearDupCheck{ctx: context.Background(), remaining: 150}
	chunks, notes := check.chunkCandidates(repo, []candidate{a, b})
	if len(chunks) != 1 || check.remaining != 50 || len(notes) != 1 || !strings.Contains(notes[0], "byte budget") {
		t.Fatalf("byte budget: chunks=%d remaining=%d notes=%v", len(chunks), check.remaining, notes)
	}
	// Enforce the streaming limit even if supplied candidate metadata is stale.
	check.remaining = 10
	if refs, err := check.chunkCandidate(repo, a.blob); err != errNearDupBytes || refs != nil || check.remaining != 0 {
		t.Fatalf("streaming byte budget: refs=%v error=%v remaining=%d", refs, err, check.remaining)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	check = &nearDupCheck{ctx: ctx, remaining: nearDupByteBudget}
	chunks, notes = check.chunkCandidates(repo, []candidate{a})
	if len(chunks) != 0 || len(notes) != 1 || !strings.Contains(notes[0], "time budget") {
		t.Fatalf("time budget: %v", notes)
	}
	if got, notes := check.findCandidates(repo, 100, DefaultNearDupConfig()); len(got) != 0 || len(notes) != 1 {
		t.Fatalf("discovery deadline: %v", notes)
	}
	// Delete a real loose HEAD blob to model an unavailable partial-clone object.
	objectPath := filepath.Join(repo, ".git", "objects", a.blob[:2], a.blob[2:])
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	check = &nearDupCheck{ctx: context.Background(), remaining: nearDupByteBudget}
	chunks, notes = check.chunkCandidates(repo, []candidate{a, b})
	if len(chunks) != 1 || len(notes) != 1 || !strings.Contains(notes[0], "unavailable locally") {
		t.Fatalf("missing blob: chunks=%d notes=%v", len(chunks), notes)
	}
}

func TestNearDupGitEnvironment(t *testing.T) {
	check, cancel := newNearDupCheck()
	defer cancel()
	cmd := check.command(t.TempDir(), "ls-tree", "HEAD")
	for _, want := range gitEnv {
		found := false
		for _, entry := range cmd.Env {
			if entry == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing environment setting %q", want)
		}
	}
}
