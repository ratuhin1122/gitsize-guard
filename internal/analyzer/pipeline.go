package analyzer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Kind says which set of files a Selection covers.
type Kind int

const (
	// KindPaths is the modified or untracked (and, unless Force, not ignored)
	// files matching Pathspecs: what `git add <pathspec>...` stages.
	KindPaths Kind = iota
	// KindTracked is the tracked files with unstaged changes matching
	// Pathspecs: what `git add -u`, `git commit -a` or `git commit <paths>`
	// picks up.
	KindTracked
	// KindStaged is the changes already in the index: what `git commit`
	// records.
	KindStaged
	// KindPending is everything `git add -A && git commit` would record. It is
	// the fallback when a command cannot be analyzed precisely.
	KindPending
	// KindFiles is exactly the named files, whatever their status.
	KindFiles
)

// Selection names a set of files in the repository containing Dir.
type Selection struct {
	Dir       string // directory git runs in; relative pathspecs resolve against it
	Kind      Kind
	Pathspecs []string // git pathspecs (KindPaths, KindTracked) or file paths (KindFiles)
	Force     bool     // include ignored files, as `git add -f` does
	// Renormalize (KindPaths) covers every tracked file matching Pathspecs,
	// as `git add --renormalize` re-stages files that look unchanged.
	Renormalize bool
}

// preciseFloor is the size below which a file's growth is taken to be its
// size, without hashing it to look for content git already has.
const preciseFloor = 1 << 20

// maxStoreFiles caps how many filtered files one check runs filters on.
const maxStoreFiles = 64

// maxReportedFiles caps Report.Files.
const maxReportedFiles = 20

// Analyze works out how many bytes the selected files would add to their
// repositories' object stores and classifies the growth of each repository,
// returning the report for the worst one. It never writes to a repository.
// Selections outside any git work tree are ignored. If some selections cannot
// be analyzed, it returns their errors together with a report on the rest.
func Analyze(sels []Selection, t Thresholds) (*Report, error) {
	scans := map[string]*repoScan{}
	var roots []string
	var errs []error
	for _, s := range sels {
		root, err := RepoRoot(s.Dir)
		if err != nil {
			continue
		}
		rs, ok := scans[root]
		if !ok {
			rs = &repoScan{root: root, cands: map[string]*candidate{}}
			scans[root] = rs
			roots = append(roots, root)
		}
		if err := rs.add(s); err != nil {
			errs = append(errs, err)
		}
	}

	var worst *Report
	for _, root := range roots {
		files, err := scans[root].measure()
		if err != nil {
			errs = append(errs, err) // files is still the best available measurement
		}
		if len(roots) > 1 { // say which repository each file is in
			for i := range files {
				files[i].FilePath = filepath.ToSlash(filepath.Join(root, filepath.FromSlash(files[i].FilePath)))
			}
		}
		if rep := NewReport(files, t); worst == nil || worse(rep, worst) {
			worst = rep
		}
	}
	if worst == nil {
		worst = NewReport(nil, t)
	}
	return worst, errors.Join(errs...)
}

func worse(a, b *Report) bool {
	rank := map[string]int{RiskLow: 0, RiskMedium: 1, RiskHigh: 2}
	if rank[a.RiskLevel] != rank[b.RiskLevel] {
		return rank[a.RiskLevel] > rank[b.RiskLevel]
	}
	return a.GrowthBytes > b.GrowthBytes
}

type candidate struct {
	rel    string // slash-separated path relative to the repository root
	blob   string // staged blob id; empty for working-tree content
	change string
	rehash bool // compare with HEAD whatever its size (git add --renormalize)
}

// repoScan collects the candidate files for one repository.
type repoScan struct {
	root      string
	cands     map[string]*candidate
	order     []string
	noFilters []string // -c options that disable configured filters; nil until needed
}

// put records c. Working-tree content supersedes a staged blob for the same
// path, since staging it would replace that blob.
func (r *repoScan) put(c *candidate) {
	old, ok := r.cands[c.rel]
	if ok && old.blob == "" && c.blob != "" {
		return
	}
	if !ok {
		r.order = append(r.order, c.rel)
	}
	r.cands[c.rel] = c
}

func (r *repoScan) add(s Selection) error {
	switch s.Kind {
	case KindStaged:
		return r.addStaged()
	case KindPending:
		if err := r.addStaged(); err != nil {
			return err
		}
		flags := []string{"--modified", "--others"}
		if !s.Force {
			flags = append(flags, "--exclude-standard")
		}
		return r.addListed(s.Dir, flags, []string{":/"}, false)
	case KindPaths:
		if s.Renormalize {
			return r.addListed(s.Dir, []string{"--cached"}, s.Pathspecs, true)
		}
		flags := []string{"--modified", "--others"}
		if !s.Force {
			flags = append(flags, "--exclude-standard")
		}
		return r.addListed(s.Dir, flags, s.Pathspecs, false)
	case KindTracked:
		return r.addListed(s.Dir, []string{"--modified"}, s.Pathspecs, false)
	case KindFiles:
		return r.addFiles(s.Dir, s.Pathspecs)
	}
	return fmt.Errorf("unknown selection kind %d", s.Kind)
}

func (r *repoScan) addStaged() error {
	blobs, err := stagedBlobs(r.root)
	if err != nil {
		return err
	}
	for _, b := range blobs {
		r.put(&candidate{rel: b.path, blob: b.blob, change: ChangeStaged})
	}
	return nil
}

// addListed adds the files `git ls-files <flags> -- <pathspecs>` lists in dir.
func (r *repoScan) addListed(dir string, flags, pathspecs []string, rehash bool) error {
	if len(pathspecs) == 0 {
		return nil
	}
	if r.noFilters == nil {
		// ls-files --modified re-hashes files whose stat data changed, which
		// would run clean filters such as Git LFS (copying content into
		// .git/lfs). Turn them off for listing; a file then counts as
		// modified at worst, and is measured properly below.
		drivers, err := filterDrivers(r.root)
		if err != nil {
			return err
		}
		r.noFilters = []string{}
		for name := range drivers {
			r.noFilters = append(r.noFilters, "-c", "filter."+name+".clean=", "-c", "filter."+name+".process=",
				"-c", "filter."+name+".required=false")
		}
	}
	args := append(append(append([]string{}, flags...), "--"), pathspecs...)
	entries, err := listFiles(dir, r.noFilters, args...)
	if err != nil {
		return err
	}
	for _, e := range entries {
		change := ChangeModified
		if e[0] == "?" {
			change = ChangeAdded
		}
		r.put(&candidate{rel: e[1], change: change, rehash: rehash})
	}
	return nil
}

func (r *repoScan) addFiles(dir string, files []string) error {
	realRoot, err := filepath.EvalSymlinks(r.root)
	if err != nil {
		return err
	}
	for _, f := range files {
		abs := f
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(dir, abs)
		}
		info, err := os.Lstat(abs)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("%s is a directory: pass individual files, or use --pending", f)
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(realRoot, filepath.Join(parent, filepath.Base(abs)))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s is outside the repository at %s", f, r.root)
		}
		r.put(&candidate{rel: filepath.ToSlash(rel), change: ChangeFile})
	}
	return nil
}

// maxHashBytes bounds how much working-tree content measure hashes to find
// content already in HEAD; files beyond it count at face value.
const maxHashBytes = 1 << 30

// measure sizes every candidate. Content already in HEAD counts as zero;
// LFS-tracked files count as their pointer; other filtered or re-encoded
// files are measured in a throwaway object directory. Small unfiltered files
// count at face value without being hashed.
func (r *repoScan) measure() ([]FileImpact, error) {
	var impacts []FileImpact
	var worktree, staged []int // indexes into impacts
	blobOf := map[int]string{}
	rehash := map[int]bool{}

	for _, rel := range r.order {
		c := r.cands[rel]
		fi := FileImpact{FilePath: rel, ChangeType: c.change}
		rehash[len(impacts)] = c.rehash
		if c.blob != "" {
			blobOf[len(impacts)] = c.blob
			staged = append(staged, len(impacts))
		} else {
			info, err := os.Lstat(r.abs(rel))
			if err != nil {
				continue // deleted: adds nothing
			}
			mode := info.Mode()
			if !mode.IsRegular() && mode&os.ModeSymlink == 0 {
				continue // e.g. a nested repository
			}
			fi.SizeBytes, fi.GrowthBytes = info.Size(), info.Size()
			if mode.IsRegular() {
				worktree = append(worktree, len(impacts))
			}
		}
		impacts = append(impacts, fi)
	}

	var head map[string]bool // read lazily: only needed for staged or large files
	inHead := func(id string) bool {
		if head == nil {
			head = readHead(r.root)
		}
		return head[id]
	}

	if len(staged) > 0 {
		ids := make([]string, 0, len(staged))
		for _, i := range staged {
			ids = append(ids, blobOf[i])
		}
		sizes, err := objectSizes(r.root, nil, ids)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, i := range staged {
			id := blobOf[i]
			n := sizes[id]
			impacts[i].SizeBytes, impacts[i].GrowthBytes = n, n
			if seen[id] || inHead(id) { // stored once; or already in HEAD, e.g. a rename
				impacts[i].GrowthBytes = 0
			}
			seen[id] = true
		}
	}

	err := r.refineWorktree(impacts, worktree, rehash, inHead)

	for i := range impacts {
		if impacts[i].GrowthBytes >= preciseFloor {
			impacts[i].IsBinary, _ = IsLikelyBinary(r.abs(impacts[i].FilePath))
		}
	}
	return impacts, err
}

// refineWorktree adjusts the growth of working-tree files that git transforms
// when staging (LFS, other filters, encodings), and hashes large plain files
// to find content already in HEAD. Hashing and filter runs share a budget;
// files beyond it keep their face value. Errors leave face values in place.
func (r *repoScan) refineWorktree(impacts []FileImpact, idx []int, rehash map[int]bool, inHead func(string) bool) error {
	if len(idx) == 0 {
		return nil
	}
	paths := make([]string, len(idx))
	impactOf := make(map[string]int, len(idx))
	for k, i := range idx {
		paths[k] = impacts[i].FilePath
		impactOf[paths[k]] = i
	}
	var errs []error
	conv, err := conversions(r.root, paths)
	if err != nil {
		errs = append(errs, err)
	}

	var toStore, toHash []string
	budget := int64(maxHashBytes)
	take := func(n int64) bool {
		if n > budget {
			return false
		}
		budget -= n
		return true
	}
	for _, p := range paths {
		i := impactOf[p]
		fi := &impacts[i]
		switch conv[p] {
		case convLFS:
			fi.GrowthBytes = lfsPointerSize(fi.SizeBytes)
		case convFilter, convEncoding:
			if fi.SizeBytes >= preciseFloor && len(toStore) < maxStoreFiles && take(fi.SizeBytes) {
				toStore = append(toStore, p)
			}
		default:
			if (fi.SizeBytes >= preciseFloor || rehash[i]) && take(fi.SizeBytes) {
				toHash = append(toHash, p)
			}
		}
	}

	if len(toHash) > 0 {
		if ids, err := hashPaths(r.root, nil, false, toHash); err != nil {
			errs = append(errs, err)
		} else {
			for k, p := range toHash {
				if inHead(ids[k]) {
					impacts[impactOf[p]].GrowthBytes = 0
				}
			}
		}
	}
	if len(toStore) > 0 {
		if blobs, err := storedBlobs(r.root, toStore); err != nil {
			errs = append(errs, err)
		} else {
			for p, b := range blobs {
				impacts[impactOf[p]].GrowthBytes = b.size
				if inHead(b.id) {
					impacts[impactOf[p]].GrowthBytes = 0
				}
			}
		}
	}
	return errors.Join(errs...)
}

// WriteGrowth estimates what writing size bytes to rel (relative to root)
// would add once committed, honouring Git LFS. exact is false when another
// filter or encoding applies, whose output can't be known without the file.
func WriteGrowth(root, rel string, size int64) (growth int64, exact bool, err error) {
	conv, err := conversions(root, []string{filepath.ToSlash(rel)})
	if err != nil {
		return size, false, err
	}
	switch conv[filepath.ToSlash(rel)] {
	case convLFS:
		return lfsPointerSize(size), true, nil
	case convFilter, convEncoding:
		return size, false, nil
	}
	return size, true, nil
}

func (r *repoScan) abs(rel string) string {
	return filepath.Join(r.root, filepath.FromSlash(rel))
}

// NewReport sorts files by growth (largest first) and classifies their total.
func NewReport(files []FileImpact, t Thresholds) *Report {
	sort.SliceStable(files, func(i, j int) bool { return files[i].GrowthBytes > files[j].GrowthBytes })

	var total int64
	for _, f := range files {
		total += f.GrowthBytes
	}
	risk, reasons := ClassifyRisk(total, t)

	report := &Report{
		RiskLevel:     risk,
		GrowthBytes:   total,
		FilesAnalyzed: len(files),
		Files:         []FileImpact{},
		Reasons:       reasons,
	}
	if len(files) > maxReportedFiles {
		files = files[:maxReportedFiles]
	}
	report.Files = append(report.Files, files...)
	if len(report.Files) > 0 {
		largest := report.Files[0]
		report.LargestFile = &largest
	}
	report.Recommendation = RecommendAction(risk, report.LargestFile, total)
	return report
}
