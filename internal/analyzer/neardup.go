package analyzer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ratuhin1122/gitsize-guard/internal/similarity"
)

// NearDupConfig controls near-duplicate detection.
type NearDupConfig struct {
	Enabled   bool
	Threshold float64
	MinBytes  int64
}

// DefaultNearDupConfig returns the default near-duplicate settings.
func DefaultNearDupConfig() NearDupConfig {
	return NearDupConfig{Enabled: true, Threshold: 0.90, MinBytes: 5 << 20}
}

const (
	nearDupByteBudget    = 512 << 20
	nearDupTimeBudget    = 5 * time.Second
	maxNearDupCandidates = 5
	maxLFSPointerBytes   = 1024
)

var errNearDupBytes = errors.New("near-duplicate byte budget reached")

// nearDupCheck owns one deadline and read allowance across discovery and all
// candidate reads. Callers doing a multi-candidate check must reuse this object.
type nearDupCheck struct {
	ctx       context.Context
	remaining int64
}

func newNearDupCheck() (*nearDupCheck, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), nearDupTimeBudget)
	return &nearDupCheck{ctx: ctx, remaining: nearDupByteBudget}, cancel
}

func (c *nearDupCheck) command(repo string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(c.ctx, "git", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), gitEnv...)
	cmd.WaitDelay = 250 * time.Millisecond
	return cmd
}

// Git launchers can leave a child holding a pipe after the launcher is killed
// on Windows. Closing our read end makes a blocked read honor the deadline.
func cancelGitStream(cmd *exec.Cmd, stdout io.Closer) {
	cmd.Cancel = func() error {
		_ = stdout.Close()
		return cmd.Process.Kill()
	}
}

func findCandidates(repo string, newSize int64, cfg NearDupConfig) (candidates []candidate, notes []string) {
	check, cancel := newNearDupCheck()
	defer cancel()
	return check.findCandidates(repo, newSize, cfg)
}

func (c *nearDupCheck) findCandidates(repo string, newSize int64, cfg NearDupConfig) ([]candidate, []string) {
	if !cfg.Enabled || newSize <= 0 {
		return nil, nil
	}
	// Distinguish an unborn HEAD from a failure to enumerate an existing tree.
	if err := c.command(repo, "rev-parse", "--verify", "HEAD").Run(); err != nil {
		if c.ctx.Err() != nil {
			return nil, []string{"Near-duplicate discovery skipped: time budget reached."}
		}
		if err := c.command(repo, "symbolic-ref", "-q", "HEAD").Run(); err == nil {
			return nil, nil
		}
		return nil, []string{"Near-duplicate discovery skipped: HEAD is unavailable."}
	}
	cmd := c.command(repo, "ls-tree", "-r", "-l", "-z", "HEAD")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, []string{fmt.Sprintf("Near-duplicate discovery skipped: %v", err)}
	}
	cancelGitStream(cmd, stdout)
	if err := cmd.Start(); err != nil {
		return nil, []string{fmt.Sprintf("Near-duplicate discovery skipped: %v", err)}
	}
	var candidates []candidate
	var notes []string
	reader := bufio.NewReader(stdout)
	for {
		record, readErr := reader.ReadString(0)
		if readErr != nil {
			break
		}
		meta, path, ok := strings.Cut(strings.TrimSuffix(record, "\x00"), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 4 || fields[1] != "blob" {
			continue
		}
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil || size < 0 {
			notes = append(notes, fmt.Sprintf("Skipped near-duplicate candidate %q: blob unavailable locally.", path))
			continue
		}
		// Avoid overflow in the size ratio comparisons.
		if size < cfg.MinBytes || size < newSize/2+newSize%2 || (size > newSize && size-newSize > newSize) {
			continue
		}
		candidates = append(candidates, candidate{rel: path, blob: fields[2], size: size})
	}
	if err := cmd.Wait(); err != nil {
		if c.ctx.Err() != nil {
			return nil, []string{"Near-duplicate discovery skipped: time budget reached."}
		}
		return nil, []string{fmt.Sprintf("Near-duplicate discovery skipped: %v (%s)", err, strings.TrimSpace(stderr.String()))}
	}
	distance := func(size int64) int64 {
		if size > newSize {
			return size - newSize
		}
		return newSize - size
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := distance(candidates[i].size), distance(candidates[j].size)
		if a != b {
			return a < b
		}
		return candidates[i].rel < candidates[j].rel
	})
	var selected []candidate
	for _, cand := range candidates {
		if c.ctx.Err() != nil {
			notes = append(notes, "Near-duplicate discovery stopped: time budget reached.")
			break
		}
		if cand.size <= maxLFSPointerBytes {
			var content bytes.Buffer
			if err := c.readBlob(repo, cand.blob, func(r io.Reader) error { _, err := io.Copy(&content, r); return err }); err != nil {
				notes = append(notes, skipNearDupNote(cand, err))
				continue
			}
			if strings.HasPrefix(content.String(), "version https://git-lfs.github.com/spec/v1\n") {
				continue
			}
		}
		selected = append(selected, cand)
		if len(selected) == maxNearDupCandidates {
			break
		}
	}
	return selected, notes
}

type nearDupReader struct {
	check  *nearDupCheck
	reader io.Reader
}

func (r nearDupReader) Read(p []byte) (int, error) {
	if err := r.check.ctx.Err(); err != nil {
		return 0, err
	}
	if r.check.remaining == 0 {
		return 0, errNearDupBytes
	}
	if int64(len(p)) > r.check.remaining {
		p = p[:int(r.check.remaining)]
	}
	n, err := r.reader.Read(p)
	r.check.remaining -= int64(n)
	return n, err
}

func (c *nearDupCheck) readBlob(repo, oid string, consume func(io.Reader) error) error {
	if err := c.ctx.Err(); err != nil {
		return err
	}
	if c.remaining <= 0 {
		return errNearDupBytes
	}
	cmd := c.command(repo, "cat-file", "blob", oid)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cancelGitStream(cmd, stdout)
	if err := cmd.Start(); err != nil {
		return err
	}
	readErr := consume(nearDupReader{check: c, reader: stdout})
	if readErr != nil {
		_ = stdout.Close()
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if c.ctx.Err() != nil {
		return c.ctx.Err()
	}
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("blob unavailable locally: %w (%s)", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func chunkCandidate(repo, oid string) ([]similarity.ChunkRef, error) {
	check, cancel := newNearDupCheck()
	defer cancel()
	return check.chunkCandidate(repo, oid)
}

func (c *nearDupCheck) chunkCandidate(repo, oid string) ([]similarity.ChunkRef, error) {
	var chunks []similarity.ChunkRef
	err := c.readBlob(repo, oid, func(r io.Reader) error {
		var err error
		chunks, err = similarity.Chunk(r)
		return err
	})
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

func skipNearDupNote(cand candidate, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return fmt.Sprintf("Skipped near-duplicate candidate %q: time budget reached.", cand.rel)
	}
	return fmt.Sprintf("Skipped near-duplicate candidate %q: %v.", cand.rel, err)
}

// chunkCandidates shares a single budget across the candidates. Missing local
// blobs and exhausted budgets produce notes rather than aborting the check.
func (c *nearDupCheck) chunkCandidates(repo string, candidates []candidate) (map[string][]similarity.ChunkRef, []string) {
	chunks := make(map[string][]similarity.ChunkRef)
	var notes []string
	for _, cand := range candidates {
		if _, ok := chunks[cand.blob]; ok {
			continue
		}
		if cand.size > c.remaining {
			notes = append(notes, skipNearDupNote(cand, errNearDupBytes))
			continue
		}
		refs, err := c.chunkCandidate(repo, cand.blob)
		if err != nil {
			notes = append(notes, skipNearDupNote(cand, err))
			continue
		}
		chunks[cand.blob] = refs
	}
	return chunks, notes
}

func nearDuplicateReason(file FileImpact) string {
	return fmt.Sprintf("%s is ~%.0f%% identical to %s already in HEAD", file.FilePath, file.NearDuplicate.Similarity*100, file.NearDuplicate.Path)
}

// CheckNearDuplicate annotates a file using supplied content, without changing
// its measured size or growth. The caller builds a report to apply risk policy.
// Content is consumed only when detection is enabled and the file is eligible.
func CheckNearDuplicate(repo string, file *FileImpact, content io.Reader, cfg NearDupConfig) []string {
	if !cfg.Enabled || file.SizeBytes < cfg.MinBytes {
		return nil
	}
	check, cancel := newNearDupCheck()
	defer cancel()
	return check.annotate(repo, file, cfg, func() ([]similarity.ChunkRef, error) {
		return similarity.Chunk(nearDupReader{check: check, reader: content})
	})
}

func (c *nearDupCheck) annotate(repo string, file *FileImpact, cfg NearDupConfig, readNew func() ([]similarity.ChunkRef, error)) []string {
	candidates, notes := c.findCandidates(repo, file.SizeBytes, cfg)
	// Updating a path's own version is not adding a duplicate copy.
	filtered := candidates[:0]
	for _, cand := range candidates {
		if cand.rel != filepath.ToSlash(file.FilePath) {
			filtered = append(filtered, cand)
		}
	}
	if len(filtered) == 0 {
		return notes
	}
	newChunks, err := readNew()
	if err != nil {
		return append(notes, skipNearDupNote(candidate{rel: file.FilePath}, err))
	}
	oldChunks, skipped := c.chunkCandidates(repo, filtered)
	notes = append(notes, skipped...)
	for _, cand := range filtered {
		chunks, ok := oldChunks[cand.blob]
		if !ok {
			continue
		}
		result := similarity.Compare(newChunks, chunks)
		if result.Similarity >= cfg.Threshold && result.NewSize > 0 && (file.NearDuplicate == nil || result.Similarity > file.NearDuplicate.Similarity) {
			file.NearDuplicate = &NearDuplicate{Path: cand.rel, Similarity: result.Similarity, SharedBytes: result.SharedBytes, CandidateSize: cand.size}
		}
	}
	return notes
}

func (r *repoScan) nearDuplicates(files []FileImpact, cfg NearDupConfig) []string {
	if !cfg.Enabled {
		return nil
	}
	check, cancel := newNearDupCheck()
	defer cancel()
	var notes []string
	for i := range files {
		file := &files[i]
		if file.SizeBytes < cfg.MinBytes {
			continue
		}
		cand := r.cands[file.FilePath]
		if cand == nil {
			continue
		}
		notes = append(notes, check.annotate(r.root, file, cfg, func() ([]similarity.ChunkRef, error) {
			if cand.blob != "" {
				return check.chunkCandidate(r.root, cand.blob)
			}
			path := r.abs(file.FilePath)
			info, err := os.Lstat(path)
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("not a regular file")
			}
			reader, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			defer reader.Close()
			return similarity.Chunk(nearDupReader{check: check, reader: reader})
		})...)
	}
	return notes
}
