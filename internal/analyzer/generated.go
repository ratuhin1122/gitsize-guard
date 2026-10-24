package analyzer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

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
