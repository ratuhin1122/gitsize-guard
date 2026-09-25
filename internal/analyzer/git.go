package analyzer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// gitEnv keeps every git call local and free of side effects: no lazy fetches
// from a partial clone's promisor remote, and no opportunistic index refresh.
var gitEnv = []string{"GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0"}

// runGit runs git in dir with optional stdin and extra environment, and
// returns stdout. Errors include git's stderr.
func runGit(dir string, stdin []byte, env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), gitEnv...), env...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s failed: %w\nstderr: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// RunGitCommand runs `git <args...>` with Dir set to repoPath.
// Returns trimmed stdout, or an error including stderr content on failure.
func RunGitCommand(repoPath string, args ...string) (string, error) {
	out, err := runGit(repoPath, nil, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// RepoRoot returns the top-level directory of the work tree containing dir.
func RepoRoot(dir string) (string, error) {
	out, err := runGit(dir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	// Only strip git's newline: a directory name may itself end in whitespace.
	root := strings.TrimSuffix(string(out), "\n")
	if root == "" {
		return "", errors.New("not inside a git work tree")
	}
	return filepath.FromSlash(root), nil
}

// listFiles runs `git ls-files -t` in dir (with extra leading git options,
// e.g. -c settings) and returns status tags and repository-relative paths.
func listFiles(dir string, gitOpts []string, args ...string) ([][2]string, error) {
	full := append(append(append([]string{}, gitOpts...), "ls-files", "-z", "-t", "--full-name"), args...)
	out, err := runGit(dir, nil, nil, full...)
	if err != nil {
		return nil, err
	}
	var entries [][2]string
	for _, rec := range bytes.Split(out, []byte{0}) {
		if tag, path, ok := strings.Cut(string(rec), " "); ok {
			entries = append(entries, [2]string{tag, path})
		}
	}
	return entries, nil
}

type stagedBlob struct {
	path, blob string
}

// stagedBlobs lists index entries that differ from HEAD (added, modified, or
// type-changed) with the blob each would commit. Submodules are skipped.
func stagedBlobs(root string) ([]stagedBlob, error) {
	out, err := runGit(root, nil, nil, "diff", "--cached", "--raw", "-z", "--no-abbrev",
		"--no-renames", "--no-color", "--no-ext-diff", "--ignore-submodules", "--diff-filter=AMT")
	if err != nil {
		return nil, err
	}
	// Records are ":<old mode> <new mode> <old id> <new id> <status>" NUL <path> NUL.
	fields := bytes.Split(out, []byte{0})
	var blobs []stagedBlob
	for i := 0; i+1 < len(fields); i += 2 {
		meta := strings.Fields(string(fields[i]))
		if len(meta) < 5 || meta[1] == "160000" {
			continue
		}
		blobs = append(blobs, stagedBlob{path: string(fields[i+1]), blob: meta[3]})
	}
	return blobs, nil
}

// objectSizes returns the size of each object id that exists.
func objectSizes(dir string, env, ids []string) (map[string]int64, error) {
	sizes := map[string]int64{}
	if len(ids) == 0 {
		return sizes, nil
	}
	out, err := runGit(dir, []byte(strings.Join(ids, "\n")+"\n"), env,
		"cat-file", "--batch-check=%(objectname) %(objectsize)")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue // includes "<id> missing"
		}
		if n, err := strconv.ParseInt(f[1], 10, 64); err == nil {
			sizes[f[0]] = n
		}
	}
	return sizes, nil
}

// readHead returns the set of blob ids in HEAD's tree. It reads only trees
// (never blob contents), so it is cheap and needs no network in a partial
// clone. An unborn HEAD, or a tree that cannot be read, yields an empty set,
// so nothing is wrongly treated as already stored.
func readHead(root string) map[string]bool {
	blobs := map[string]bool{}
	out, err := runGit(root, nil, nil, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if err != nil {
		return blobs
	}
	// Records are "<mode> <type> <id>" TAB <path>.
	for _, rec := range bytes.Split(out, []byte{0}) {
		meta, _, ok := bytes.Cut(rec, []byte{'\t'})
		if !ok {
			continue
		}
		if f := strings.Fields(string(meta)); len(f) == 3 && f[1] == "blob" {
			blobs[f[2]] = true
		}
	}
	return blobs
}

// hashPaths returns the blob id git would store for each path (relative to
// root). Without write it stores nothing; it is only used without write on
// files that have no filter, since a filter such as Git LFS keeps its own copy.
func hashPaths(root string, env []string, write bool, paths []string) ([]string, error) {
	args := []string{"hash-object", "--stdin-paths"}
	if write {
		args = []string{"hash-object", "-w", "--stdin-paths"}
	}
	var in strings.Builder
	for _, p := range paths {
		in.WriteString(stdinPath(p))
		in.WriteByte('\n')
	}
	out, err := runGit(root, []byte(in.String()), env, args...)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(out))
	if len(ids) != len(paths) {
		return nil, fmt.Errorf("git hash-object returned %d ids for %d paths", len(ids), len(paths))
	}
	return ids, nil
}

// Conversion kinds reported by conversions.
const (
	convLFS      = "lfs"      // Git LFS: git stores a small pointer
	convFilter   = "filter"   // another clean filter: stored size unknown until run
	convEncoding = "encoding" // working-tree-encoding: git re-encodes the content
)

// conversions reports which paths git transforms when staging them: a clean
// filter whose driver is actually configured, or a working-tree-encoding.
func conversions(root string, paths []string) (map[string]string, error) {
	res := map[string]string{}
	if len(paths) == 0 {
		return res, nil
	}
	drivers, err := filterDrivers(root)
	if err != nil {
		return nil, err
	}
	var in bytes.Buffer
	for _, p := range paths {
		in.WriteString(p)
		in.WriteByte(0)
	}
	out, err := runGit(root, in.Bytes(), nil, "check-attr", "-z", "--stdin", "filter", "working-tree-encoding")
	if err != nil {
		return nil, err
	}
	// Records are <path> NUL <attribute> NUL <value> NUL.
	fields := bytes.Split(out, []byte{0})
	for i := 0; i+2 < len(fields); i += 3 {
		path, attr, value := string(fields[i]), string(fields[i+1]), string(fields[i+2])
		switch value {
		case "unspecified", "unset", "set":
			continue
		}
		switch {
		case attr == "filter" && value == "lfs" && drivers["lfs"]:
			res[path] = convLFS
		case attr == "filter" && drivers[value]:
			res[path] = convFilter
		case attr == "working-tree-encoding" && res[path] == "":
			res[path] = convEncoding
		}
	}
	return res, nil
}

// filterDrivers returns the names of filters with a clean or process command
// configured. A filter attribute without one leaves content unchanged.
func filterDrivers(root string) (map[string]bool, error) {
	drivers := map[string]bool{}
	out, err := runGit(root, nil, nil, "config", "-z", "--get-regexp", `^filter\..*\.(clean|process)$`)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return drivers, nil // no matching keys
		}
		return nil, err
	}
	// Entries are "<key>\n<value>" NUL.
	for _, entry := range bytes.Split(out, []byte{0}) {
		key, _, _ := bytes.Cut(entry, []byte{'\n'})
		k := strings.TrimPrefix(string(key), "filter.")
		if dot := strings.LastIndex(k, "."); dot > 0 {
			drivers[k[:dot]] = true
		}
	}
	return drivers, nil
}

// lfsPointerSize is the size of the Git LFS pointer file git stores for n
// bytes of content.
func lfsPointerSize(n int64) int64 {
	const fixed = len("version https://git-lfs.github.com/spec/v1\n") +
		len("oid sha256:") + 64 + len("\n") + len("size ") + len("\n")
	return int64(fixed + len(strconv.FormatInt(n, 10)))
}

// storedBlobs hashes paths into a throwaway object directory, as git add
// would (applying filters and encodings), and returns the id and size of each
// resulting blob. The repository's own object store is never touched.
func storedBlobs(root string, paths []string) (map[string]storedBlob, error) {
	removeStaleQuarantines()
	tmp, err := os.MkdirTemp("", quarantinePrefix)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	env := []string{"GIT_OBJECT_DIRECTORY=" + tmp}
	ids, err := hashPaths(root, env, true, paths)
	if err != nil {
		return nil, err
	}
	sizes, err := objectSizes(root, env, ids)
	if err != nil {
		return nil, err
	}
	res := make(map[string]storedBlob, len(paths))
	for i, p := range paths {
		if n, ok := sizes[ids[i]]; ok {
			res[p] = storedBlob{id: ids[i], size: n}
		}
	}
	return res, nil
}

type storedBlob struct {
	id   string
	size int64
}

const quarantinePrefix = "gitsize-quarantine-"

// removeStaleQuarantines deletes throwaway object directories left behind by
// runs that were killed (e.g. by the hook timeout) before cleaning up.
func removeStaleQuarantines() {
	dirs, _ := filepath.Glob(filepath.Join(os.TempDir(), quarantinePrefix+"*"))
	for _, d := range dirs {
		if info, err := os.Stat(d); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.RemoveAll(d)
		}
	}
}

// stdinPath formats a path for `git hash-object --stdin-paths`, which
// C-unquotes lines that start with a quote and drops a trailing CR.
func stdinPath(p string) string {
	if !strings.HasPrefix(p, `"`) && !strings.ContainsAny(p, "\r\n") {
		return p
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", `\r`, "\n", `\n`, "\t", `\t`).Replace(p) + `"`
}
