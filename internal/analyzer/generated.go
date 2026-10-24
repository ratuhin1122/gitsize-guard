package analyzer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ratuhin1122/gitsize-guard/internal/generated"
)

// GeneratedConfig controls generated-file advisories independently of similarity.
type GeneratedConfig struct {
	Enabled    bool
	StrictWeak bool
	// A nil Allow loads .gitsizeallow; a non-nil list overrides it.
	Allow []string
	// Nonpositive limits retain the production defaults (5 seconds / 5000
	// paths). Tests can override either limit without changing those defaults.
	TimeBudget time.Duration
	MaxPaths   int
}

func DefaultGeneratedConfig() GeneratedConfig { return GeneratedConfig{Enabled: true} }

const (
	generatedTimeBudget = 5 * time.Second
	generatedPathCap    = 5000
)

// generatedCheck shares a deadline and records nonfatal skips. Keep this object
// for a whole generated-file check so notes can be attached to its report.
type generatedCheck struct {
	ctx      context.Context
	maxPaths int
	notes    []string
	head     map[string][]byte
}

func newGeneratedCheck(budget time.Duration, maxPaths int) (*generatedCheck, context.CancelFunc) {
	if budget <= 0 {
		budget = generatedTimeBudget
	}
	if maxPaths <= 0 {
		maxPaths = generatedPathCap
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	return &generatedCheck{ctx: ctx, maxPaths: maxPaths, head: make(map[string][]byte)}, cancel
}

func trackedPrefixes(repo string) (map[string]bool, error) {
	check, cancel := newGeneratedCheck(0, 0)
	defer cancel()
	return check.trackedPrefixes(repo)
}

func ignoredByGitignore(repo string, paths []string) (map[string]bool, error) {
	check, cancel := newGeneratedCheck(0, 0)
	defer cancel()
	return check.ignoredByGitignore(repo, paths)
}

func (c *generatedCheck) skipped() bool {
	if c.ctx.Err() == nil {
		return false
	}
	c.notes = append(c.notes, "Generated-file check skipped: time budget reached.")
	return true
}

func (c *generatedCheck) capNote() {
	c.notes = append(c.notes, fmt.Sprintf("Generated-file check limited to %d paths; remaining paths were skipped.", c.maxPaths))
}

func (c *generatedCheck) trackedPrefixes(repo string) (map[string]bool, error) {
	prefixes := make(map[string]bool)
	out, cached := c.head[repo]
	if !cached {
		var err error
		out, err = listHeadTree(c.ctx, repo)
		if err != nil {
			if c.skipped() {
				return prefixes, nil
			}
			// An unborn symbolic HEAD points to a ref that does not exist.
			ref, refErr := runGitContext(c.ctx, repo, nil, nil, "symbolic-ref", "-q", "HEAD")
			if refErr == nil {
				_, verifyErr := runGitContext(c.ctx, repo, nil, nil, "show-ref", "--verify", "--quiet", strings.TrimSpace(string(ref)))
				var exit *exec.ExitError
				if errors.As(verifyErr, &exit) && exit.ExitCode() == 1 {
					return prefixes, nil
				}
			}
			if c.skipped() {
				return prefixes, nil
			}
			return prefixes, err
		}
		c.head[repo] = out
	}
	count := 0
	for _, record := range bytes.Split(out, []byte{0}) {
		meta, name, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 || fields[1] != "blob" {
			continue
		}
		if count == c.maxPaths {
			c.capNote()
			break
		}
		if c.skipped() {
			break
		}
		count++
		file := string(name)
		first, _, _ := strings.Cut(file, "/")
		prefixes[first] = true
		if dir := path.Dir(file); dir != "." {
			prefixes[dir] = true
		}
	}
	return prefixes, nil
}

func (c *generatedCheck) ignoredByGitignore(repo string, paths []string) (map[string]bool, error) {
	ignored := make(map[string]bool)
	if len(paths) > c.maxPaths {
		c.capNote()
		paths = paths[:c.maxPaths]
	}
	if len(paths) == 0 {
		return ignored, nil
	}
	var input bytes.Buffer
	for _, p := range paths {
		if strings.ContainsRune(p, 0) {
			return ignored, fmt.Errorf("invalid candidate path containing NUL")
		}
		input.WriteString(filepath.ToSlash(p))
		input.WriteByte(0)
	}
	// --no-index also reports ignore rules for files already force-added to
	// the index. Without it, Git silently excludes those tracked candidates.
	out, err := runGitContext(c.ctx, repo, input.Bytes(), nil, "check-ignore", "--no-index", "-z", "--stdin")
	if c.skipped() {
		return ignored, nil
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return ignored, err
		}
	}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			ignored[string(p)] = true
		}
	}
	return ignored, nil
}

// generatedFolder identifies the matched directory, or the containing directory
// for a filename/content rule. A parent like src/ alone does not suppress a new
// src/node_modules/ directory.
func generatedFolder(file, rule string) string {
	parts := strings.Split(filepath.ToSlash(file), "/")
	for i, part := range parts[:len(parts)-1] {
		if part == rule {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return path.Dir(filepath.ToSlash(file))
}

func generatedSuppressed(folder string, prefixes map[string]bool) bool {
	if folder == "." {
		return false
	}
	if prefixes[folder] {
		return true
	}
	for prefix := range prefixes {
		if strings.HasPrefix(prefix, folder+"/") {
			return true
		}
	}
	return false
}

func (c *generatedCheck) annotateGenerated(file *FileImpact, head []byte, prefixes, ignored map[string]bool, strictWeak bool) {
	match, ok := generated.Classify(file.FilePath)
	if ok && generatedSuppressed(generatedFolder(file.FilePath, match.Rule), prefixes) {
		return
	}
	if !ok || match.Confidence == generated.Weak {
		if contentMatch, found := generated.ClassifyContent(head); found {
			match, ok = contentMatch, true
		}
	}
	isIgnored := ignored[filepath.ToSlash(file.FilePath)]
	if !ok && isIgnored {
		match, ok = generated.Match{Category: "ignored", Confidence: generated.Strong, Rule: ".gitignore"}, true
	}
	if !ok || generatedSuppressed(generatedFolder(file.FilePath, match.Rule), prefixes) {
		return
	}
	confidence := "weak"
	if match.Confidence == generated.Strong || isIgnored {
		confidence = "strong"
	}
	file.Generated = &Generated{Category: match.Category, Confidence: confidence, Rule: match.Rule, IgnoredByGitignore: isIgnored}
	file.Generated.strictWeak = strictWeak
}

func (c *generatedCheck) blobHead(repo, oid string) ([]byte, error) {
	cmd := (&nearDupCheck{ctx: c.ctx}).command(repo, "cat-file", "blob", oid)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cancelGitStream(cmd, stdout)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	head, readErr := io.ReadAll(io.LimitReader(stdout, 2048))
	_ = stdout.Close()
	// Only the prefix is needed; do not drain the rest of a staged blob.
	if len(head) == 2048 || readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if c.ctx.Err() != nil {
		return nil, c.ctx.Err()
	}
	if readErr != nil {
		return nil, readErr
	}
	if len(head) < 2048 && waitErr != nil {
		return nil, waitErr
	}
	return head, nil
}

func (r *repoScan) generatedFiles(files []FileImpact, cfg GeneratedConfig, check *generatedCheck) []string {
	if !cfg.Enabled || len(files) == 0 {
		return nil
	}
	noteStart := len(check.notes)
	original := files
	selected := make([]FileImpact, 0, len(files))
	indices := make([]int, 0, len(files))
	for i, file := range files {
		if check.skipped() {
			return check.notes[noteStart:]
		}
		if !cfg.allows(file.FilePath) {
			selected = append(selected, file)
			indices = append(indices, i)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	defer func() {
		for i, file := range selected {
			original[indices[i]].Generated = file.Generated
		}
	}()
	files = selected
	if r.headTree != nil {
		check.head[r.root] = r.headTree
	}
	headNoteStart := len(check.notes)
	prefixes, err := check.trackedPrefixes(r.root)
	if err != nil {
		return []string{fmt.Sprintf("Generated-file check skipped: %v", err)}
	}
	// Incomplete HEAD knowledge must not flag intentionally tracked folders.
	if len(check.notes) > headNoteStart {
		return check.notes[noteStart:]
	}
	if len(files) > check.maxPaths {
		check.capNote()
		files = files[:check.maxPaths]
	}
	paths := make([]string, len(files))
	for i := range files {
		paths[i] = files[i].FilePath
	}
	ignored, err := check.ignoredByGitignore(r.root, paths)
	if err != nil {
		check.notes = append(check.notes, fmt.Sprintf("Generated-file ignore check skipped: %v", err))
		return check.notes[noteStart:]
	}
	for i := range files {
		if check.skipped() {
			break
		}
		file := &files[i]
		cand := r.cands[file.FilePath]
		if cand == nil {
			continue
		}
		if match, ok := generated.Classify(file.FilePath); ok && (match.Confidence == generated.Strong || generatedSuppressed(generatedFolder(file.FilePath, match.Rule), prefixes)) {
			check.annotateGenerated(file, nil, prefixes, ignored, cfg.StrictWeak)
			continue
		}
		var head []byte
		if cand.blob != "" {
			head, err = check.blobHead(r.root, cand.blob)
		} else {
			var info os.FileInfo
			info, err = os.Lstat(r.abs(file.FilePath))
			if err == nil && !info.Mode().IsRegular() {
				continue
			}
			if err == nil {
				var reader *os.File
				reader, err = os.Open(r.abs(file.FilePath))
				if err == nil {
					head, err = io.ReadAll(io.LimitReader(reader, 2048))
					_ = reader.Close()
				}
			}
		}
		if err != nil {
			check.notes = append(check.notes, fmt.Sprintf("Generated-file header skipped for %q: %v", file.FilePath, err))
		}
		check.annotateGenerated(file, head, prefixes, ignored, cfg.StrictWeak)
	}
	return check.notes[noteStart:]
}

// generatedStagedFiles retains advisories for selected index content that an add
// would leave unchanged. These entries have zero incremental growth, preserving
// the existing size measurement. Git pathspec filtering keeps unrelated staged
// paths outside the command's scope out of the advisory report.
func (r *repoScan) generatedStagedFiles(files []FileImpact, cfg GeneratedConfig, check *generatedCheck) ([]FileImpact, []string) {
	if !cfg.Enabled || len(r.generatedSelections) == 0 {
		return files, nil
	}
	seen := map[string]bool{}
	for _, file := range files {
		seen[file.FilePath] = true
	}
	var extra []stagedBlob
	for _, selection := range r.generatedSelections {
		if len(selection.Pathspecs) == 0 {
			continue
		}
		blobs, err := stagedBlobsMatching(check.ctx, selection.Dir, selection.Pathspecs)
		if err != nil {
			if check.skipped() {
				return files, check.notes
			}
			return files, []string{fmt.Sprintf("Generated-file staged check skipped: %v", err)}
		}
		for _, blob := range blobs {
			if seen[blob.path] || cfg.allows(blob.path) {
				continue
			}
			if cand := r.cands[blob.path]; cand != nil && cand.blob == "" {
				continue // a selected worktree deletion/replacement supersedes the index
			}
			if len(extra) == check.maxPaths {
				check.capNote()
				break
			}
			seen[blob.path] = true
			extra = append(extra, blob)
		}
	}
	ids := make([]string, len(extra))
	for i := range extra {
		ids[i] = extra[i].blob
	}
	sizes, err := objectSizesContext(check.ctx, r.root, nil, ids)
	if err != nil {
		if check.skipped() {
			return files, check.notes
		}
		return files, []string{fmt.Sprintf("Generated-file staged sizes skipped: %v", err)}
	}
	for _, blob := range extra {
		size, ok := sizes[blob.blob]
		if !ok {
			check.notes = append(check.notes, fmt.Sprintf("Generated-file staged header skipped for %q: blob unavailable locally.", blob.path))
			continue
		}
		files = append(files, FileImpact{FilePath: blob.path, SizeBytes: size, ChangeType: ChangeStaged, generatedOnly: true})
		r.cands[blob.path] = &candidate{rel: blob.path, blob: blob.blob, change: ChangeStaged}
	}
	return files, check.notes
}

// CheckGenerated annotates Write content supplied in memory. It never changes
// measured bytes; NewReport applies advisory policy and aggregation.
func CheckGenerated(repo string, file *FileImpact, head []byte, cfg GeneratedConfig) []string {
	if !cfg.Enabled {
		return nil
	}
	var err error
	cfg, err = ResolveGeneratedConfig(repo, cfg)
	if err != nil {
		return []string{fmt.Sprintf("Generated-file configuration error: %v", err)}
	}
	if cfg.allows(file.FilePath) {
		return nil
	}
	check, cancel := newGeneratedCheck(cfg.TimeBudget, cfg.MaxPaths)
	defer cancel()
	prefixes, err := check.trackedPrefixes(repo)
	if err != nil {
		return []string{fmt.Sprintf("Generated-file check skipped: %v", err)}
	}
	if len(check.notes) > 0 {
		return check.notes
	}
	ignored, err := check.ignoredByGitignore(repo, []string{file.FilePath})
	if err != nil {
		return []string{fmt.Sprintf("Generated-file ignore check skipped: %v", err)}
	}
	if check.skipped() {
		return check.notes
	}
	check.annotateGenerated(file, head[:min(len(head), 2048)], prefixes, ignored, cfg.StrictWeak)
	return check.notes
}

func applyGeneratedReport(report *Report, files []FileImpact) {
	type group struct {
		count   int
		size    int64
		folders map[string]bool
	}
	groups := map[string]*group{}
	for _, file := range files {
		if file.Generated == nil {
			continue
		}
		g := file.Generated
		if report.GeneratedByCategory == nil {
			report.GeneratedByCategory = map[string]int{}
		}
		report.GeneratedFiles++
		report.GeneratedByCategory[g.Category]++
		confidence := g.Confidence
		if g.strictWeak {
			confidence = "strong"
		}
		key := confidence + ":" + g.Category
		if groups[key] == nil {
			groups[key] = &group{folders: map[string]bool{}}
		}
		groups[key].count++
		groups[key].size += file.SizeBytes
		groups[key].folders[generatedFolder(file.FilePath, g.Rule)] = true
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	strong := false
	for _, key := range keys {
		g := groups[key]
		confidence, category, _ := strings.Cut(key, ":")
		label := category + " paths"
		if len(g.folders) == 1 {
			for folder := range g.folders {
				if folder != "." {
					label = folder + "/"
				}
			}
		}
		text := fmt.Sprintf("%s looks like generated or dependency files that usually should not be committed (%d files, %s total; %s).", label, g.count, HumanReadableSize(g.size), category)
		if confidence == "strong" {
			report.Reasons = append(report.Reasons, text)
			strong = true
		} else {
			report.Notes = append(report.Notes, "Possible generated files (weak evidence): "+text)
		}
	}
	if strong {
		if report.RiskLevel == RiskLow {
			report.RiskLevel = RiskMedium
		}
		if report.Recommendation != "" {
			report.Recommendation += " "
		}
		report.Recommendation += "Add the folder to .gitignore; if already staged, run git rm -r --cached <folder>. Keep build artifacts in release assets or CI artifacts."
	}
}
