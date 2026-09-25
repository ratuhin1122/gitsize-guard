// Package shellcmd statically inspects a shell command line for git
// invocations that stage or commit content, and reports which files each one
// would pick up.
//
// It is deliberately conservative: whenever it cannot tell exactly what a
// command stages (variables in paths, indirect invocations such as xargs), it
// widens the selection to everything pending in the repository; when it
// cannot tell where, or when files or the index may change earlier in the
// same line, it says so in the Plan so the caller can decide instead of
// guessing.
package shellcmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

// AliasFunc looks up a git alias in dir and reports whether one is defined.
type AliasFunc func(dir, name string) (string, bool)

// Plan is what a command line would stage or commit.
type Plan struct {
	Selections []analyzer.Selection
	// Unseen is set when an earlier command in the line may create or change
	// working-tree files (or add content to the index) before a later git
	// command stages or commits it. A check made before the line runs cannot
	// see that content.
	Unseen bool
	// Unresolved describes git commands that stage or commit content in a
	// place the parser cannot determine (e.g. --git-dir, submodule foreach).
	Unresolved []string
}

const maxDepth = 4

var (
	gitRe  = regexp.MustCompile(`(?i)\bgit\b`) // case-insensitive so "$GIT" is looked at too
	verbRe = regexp.MustCompile(`\b(add|stage|commit)\b`)
	envRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// prefixWords may precede a command without changing what runs.
var prefixWords = map[string]bool{
	"!": true, "{": true, "}": true, "if": true, "then": true, "else": true, "elif": true,
	"while": true, "until": true, "do": true, "time": true, "command": true,
	"builtin": true, "exec": true, "nohup": true, "env": true,
}

// keywordCmds start or end compound commands; they run nothing themselves.
var keywordCmds = map[string]bool{
	"for": true, "select": true, "case": true, "esac": true, "done": true, "fi": true,
	"in": true, "function": true,
}

// builtins are git subcommands other than add/stage/commit. None of them
// stages content itself (see the README's limitations for merge and friends),
// and git does not let aliases shadow them, so there is nothing to look up.
var builtins = map[string]bool{
	"am": true, "apply": true, "archive": true, "bisect": true, "blame": true, "branch": true, "bundle": true,
	"cat-file": true, "check-attr": true, "check-ignore": true, "checkout": true, "checkout-index": true,
	"cherry": true, "cherry-pick": true, "clean": true, "clone": true, "config": true,
	"count-objects": true, "describe": true, "diff": true, "diff-files": true,
	"diff-index": true, "diff-tree": true, "fetch": true, "for-each-ref": true,
	"format-patch": true, "fsck": true, "gc": true, "grep": true, "hash-object": true,
	"help": true, "init": true, "lfs": true, "log": true, "ls-files": true,
	"ls-remote": true, "ls-tree": true, "maintenance": true, "merge": true,
	"merge-base": true, "mv": true, "notes": true, "prune": true, "pull": true,
	"push": true, "range-diff": true, "read-tree": true, "rebase": true, "reflog": true, "remote": true,
	"repack": true, "replace": true, "reset": true, "restore": true, "rev-list": true,
	"rev-parse": true, "revert": true, "rm": true, "shortlog": true, "show": true,
	"show-branch": true, "show-ref": true, "sparse-checkout": true, "stash": true,
	"status": true, "submodule": true, "switch": true, "symbolic-ref": true, "tag": true,
	"update-index": true, "update-ref": true, "var": true, "version": true,
	"whatchanged": true, "worktree": true,
}

// safeCommands never create or change file content (unless their output is
// redirected, which is tracked separately). A few more are checked by
// argument in commandIsSafe.
var safeCommands = map[string]bool{
	":": true, "[": true, "[[": true, "alias": true, "awk": true, "b2sum": true, "basename": true,
	"cat": true, "cd": true, "chmod": true, "cmp": true, "cut": true, "date": true,
	"declare": true, "df": true, "diff": true, "dirname": true, "du": true, "echo": true,
	"egrep": true, "exit": true, "export": true, "false": true, "fgrep": true, "file": true,
	"grep": true, "hash": true, "head": true, "hostname": true, "id": true, "jq": true,
	"less": true, "local": true, "ls": true, "md5": true, "md5sum": true, "mkdir": true,
	"more": true, "popd": true, "printenv": true, "printf": true, "pushd": true, "pwd": true,
	"read": true, "readlink": true, "readonly": true, "realpath": true, "return": true,
	"rg": true, "rmdir": true, "set": true, "sha1sum": true, "sha256sum": true,
	"sha512sum": true, "shasum": true, "shift": true, "sleep": true, "stat": true,
	"tail": true, "test": true, "touch": true, "tr": true, "tree": true, "true": true,
	"type": true, "typeset": true, "uname": true, "uniq": true, "unset": true, "wait": true,
	"wc": true, "which": true, "whoami": true,
}

// shells run a script given with -c or on stdin; runners run their
// arguments as a command. Both can hide git invocations.
var (
	shells  = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}
	runners = map[string]bool{
		"xargs": true, "eval": true, "sudo": true, "doas": true, "su": true, "timeout": true,
		"nice": true, "watch": true, "parallel": true, "flock": true, "find": true,
	}
)

// state is what the commands run so far in a line may have changed.
type state struct {
	files bool // working-tree files, including .gitignore/.gitattributes
	index bool // the index: content a later git commit would record
}

// Parse returns what the git commands in command would stage or commit when
// run from cwd.
func Parse(command, cwd, home string, alias AliasFunc) Plan {
	var p Plan
	if gitRe.MatchString(command) {
		parse(&p, command, cwd, home, alias, 0, state{})
	}
	return p
}

// Selections returns Parse(...).Selections.
func Selections(command, cwd, home string, alias AliasFunc) []analyzer.Selection {
	return Parse(command, cwd, home, alias).Selections
}

// parse walks command, adding to p what it stages, and returns the state
// after it runs, given the state before.
func parse(p *Plan, command, cwd, home string, alias AliasFunc, depth int, st state) state {
	if depth > maxDepth {
		p.add([]analyzer.Selection{pending(cwd)}, st)
		return state{true, true}
	}

	lx := lex(command, home)
	dir, dirUnknown, rootRel := cwd, false, false
	var widened []analyzer.Selection // what something unanalyzable may stage, added at the end
	widen := func(d string, force bool) {
		if dirUnknown {
			p.unresolved("a command that may stage content, after a cd whose target can't be determined")
			return
		}
		if st.files || st.index {
			p.Unseen = true
		}
		sel := pending(d)
		sel.Force = force
		for _, w := range widened {
			if w.Dir == sel.Dir && w.Force == sel.Force {
				return
			}
		}
		widened = append(widened, sel)
	}
	if !lx.ok && gitRe.MatchString(command) && verbRe.MatchString(command) {
		widen(cwd, false)
	}

	type frame struct {
		dir              string
		unknown, rootRel bool
	}
	var subshells, pushd []frame
	for _, sc := range lx.cmds {
		switch {
		case sc.push:
			subshells = append(subshells, frame{dir, dirUnknown, rootRel})
			continue
		case sc.pop:
			if n := len(subshells); n > 0 {
				f := subshells[n-1]
				dir, dirUnknown, rootRel, subshells = f.dir, f.unknown, f.rootRel, subshells[:n-1]
			}
			continue
		}

		for _, body := range sc.subs { // substitutions run before their command
			st = parse(p, body, dir, home, alias, depth+1, st)
		}

		words, env := stripPrefix(sc.words)
		if len(words) > 0 {
			name := words[0]
			base := filepath.Base(name.text)
			switch {
			case !name.dynamic && keywordCmds[name.text]:
			case name.dynamic:
				if stagingVerb(words[1:]) {
					widen(dir, hasForce(words)) // e.g. "$GIT" add f, $(which git) commit -a
				}
				st = state{true, true}
			case name.text == "cd" || name.text == "pushd":
				if name.text == "pushd" {
					pushd = append(pushd, frame{dir, dirUnknown, rootRel})
				}
				target, ok, absolute, toplevel := cdTarget(dir, words[1:], home)
				switch {
				case toplevel: // cd "$(git rev-parse --show-toplevel)"
					rootRel = true
				case !ok:
					dirUnknown = true
				case absolute:
					dir, dirUnknown, rootRel = target, false, false
				case rootRel:
					dirUnknown = true // relative to a root we only know symbolically
				default:
					dir = target
				}
			case name.text == "popd":
				if n := len(pushd); n > 0 {
					f := pushd[n-1]
					dir, dirUnknown, rootRel, pushd = f.dir, f.unknown, f.rootRel, pushd[:n-1]
				}
			case base == "git":
				g := parseGit(words[1:], dir, env, alias, depth)
				if g.unresolved != "" {
					p.unresolved(g.unresolved)
				}
				if len(g.sels) > 0 {
					switch {
					case dirUnknown:
						p.unresolved("git " + g.sub + " after a cd whose target can't be determined")
					case rootRel:
						p.add(rootRelative(g.sels), st)
					default:
						p.add(g.sels, st)
					}
				}
				st.files, st.index = st.files || g.files, st.index || g.index
			case shells[base]:
				if script, ok, expands := shellScript(words[1:], sc.stdin); ok {
					st = parse(p, script, dir, home, alias, depth+1, st)
					if expands && gitRe.MatchString(script) && verbRe.MatchString(script) {
						widen(dir, false) // the outer shell expands $VAR and \ in it first
					}
				} else {
					if mentionsStaging(words) || (readsStdin(words[1:]) && gitRe.MatchString(command) && verbRe.MatchString(command)) {
						widen(dir, hasForce(words)) // e.g. echo 'git add .' | bash
					}
					st = state{true, true}
				}
			case runners[base]:
				if mentionsStaging(words) {
					widen(dir, hasForce(words)) // e.g. xargs git add, find -exec git add {} +
				}
				if base != "find" || findRunsCommands(words) {
					st = state{true, true}
				}
			default:
				if stagingTokens(words) {
					widen(dir, hasForce(words)) // an unlisted wrapper: caffeinate git add x
				}
				if !commandIsSafe(base, words) {
					st.files = true
				}
			}
		}
		if writesInto(dir, sc.writes) {
			st.files = true
		}
	}
	p.Selections = append(p.Selections, widened...)
	return st
}

// add records selections staged after the given state.
func (p *Plan) add(sels []analyzer.Selection, st state) {
	if len(sels) == 0 {
		return
	}
	if st.index || (st.files && readsWorktree(sels)) {
		p.Unseen = true
	}
	p.Selections = append(p.Selections, sels...)
}

func (p *Plan) unresolved(msg string) {
	for _, m := range p.Unresolved {
		if m == msg {
			return
		}
	}
	p.Unresolved = append(p.Unresolved, msg)
}

// readsWorktree reports whether any selection depends on working-tree files.
// What is already staged (KindStaged) only changes through the index.
func readsWorktree(sels []analyzer.Selection) bool {
	for _, s := range sels {
		if s.Kind != analyzer.KindStaged {
			return true
		}
	}
	return false
}

func pending(dir string) analyzer.Selection {
	return analyzer.Selection{Dir: dir, Kind: analyzer.KindPending}
}

// rootRelative rewrites relative pathspecs to be relative to the repository
// root (":/" magic), for commands run after cd "$(git rev-parse --show-toplevel)".
func rootRelative(sels []analyzer.Selection) []analyzer.Selection {
	out := make([]analyzer.Selection, len(sels))
	for i, s := range sels {
		out[i] = s
		if len(s.Pathspecs) == 0 {
			continue
		}
		out[i].Pathspecs = make([]string, len(s.Pathspecs))
		for k, spec := range s.Pathspecs {
			switch {
			case strings.HasPrefix(spec, ":") || filepath.IsAbs(spec):
				out[i].Pathspecs[k] = spec
			case spec == ".":
				out[i].Pathspecs[k] = ":/"
			default:
				out[i].Pathspecs[k] = ":/" + spec
			}
		}
	}
	return out
}

// repoEnv are the environment variables that make git use a different
// repository, work tree or index than the one found from the directory.
var repoEnv = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true, "GIT_OBJECT_DIRECTORY": true,
}

// gitCmd is one parsed git invocation.
type gitCmd struct {
	sels         []analyzer.Selection
	unresolved   string // set when it stages content in a place that can't be determined
	sub          string
	files, index bool // what it may change for later commands
}

func parseGit(args []word, dir string, env []word, alias AliasFunc, depth int) gitCmd {
	inline := map[string]string{} // aliases defined with -c alias.<name>=...
	var gitDir, workTree *word
	unknown := ""
	for _, e := range env {
		name, val, _ := strings.Cut(e.text, "=")
		switch {
		case !repoEnv[name]:
		case name == "GIT_DIR":
			gitDir = &word{text: val, dynamic: e.dynamic}
		case name == "GIT_WORK_TREE":
			workTree = &word{text: val, dynamic: e.dynamic}
		default:
			unknown = name
		}
	}

	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		t := a.text
		if !strings.HasPrefix(t, "-") {
			if a.dynamic {
				return gitCmd{sels: []analyzer.Selection{pending(dir)}, files: true, index: true} // git "$SUBCOMMAND"
			}
			break
		}
		if a.dynamic {
			unknown = "a git option set from a variable"
			continue
		}
		name, val, hasValue := strings.Cut(t, "=")
		switch name {
		case "-C":
			if i+1 >= len(args) {
				return gitCmd{}
			}
			i++
			if args[i].dynamic {
				unknown = "git -C with a variable path"
			} else {
				dir = resolve(dir, args[i].text)
			}
		case "-c":
			if i+1 < len(args) {
				i++
				if def, ok := strings.CutPrefix(args[i].text, "alias."); ok {
					if k, v, ok := strings.Cut(def, "="); ok {
						inline[k] = v
					}
				}
			}
		case "--git-dir", "--work-tree":
			w := &word{text: val}
			if !hasValue {
				if i+1 >= len(args) {
					return gitCmd{}
				}
				i++
				w = &args[i]
			}
			if name == "--git-dir" {
				gitDir = w
			} else {
				workTree = w
			}
		case "--namespace", "--exec-path", "--super-prefix", "--list-cmds", "--config-env":
			if !hasValue && name != "--exec-path" {
				i++
			}
		}
	}
	switch {
	case workTree != nil && workTree.dynamic, gitDir != nil && gitDir.dynamic:
		unknown = "GIT_DIR / --git-dir or --work-tree set from a variable"
	case workTree != nil:
		wt := resolve(dir, workTree.text)
		if gitDir == nil || resolve(dir, gitDir.text) != filepath.Join(wt, ".git") {
			unknown = "--work-tree (or GIT_WORK_TREE) not paired with its own .git"
		}
		dir = wt
	case gitDir != nil && filepath.Base(gitDir.text) == ".git":
		dir = filepath.Dir(resolve(dir, gitDir.text))
	case gitDir != nil:
		unknown = "--git-dir (or GIT_DIR) without a work tree"
	}
	if i >= len(args) {
		return gitCmd{}
	}

	sub, rest := args[i].text, args[i+1:]
	g := gitCmd{sub: sub}
	g.files, g.index = gitEffects(sub, rest)
	switch sub {
	case "add", "stage":
		g.sels = parseAdd(rest, dir)
	case "commit":
		g.sels = parseCommit(rest, dir)
	case "rebase", "submodule", "bisect":
		if mentionsStaging(rest) {
			g.unresolved = "git " + sub + " running commands that stage or commit content"
		}
		return g
	default:
		if builtins[sub] {
			return g
		}
		val, ok := inline[sub]
		if !ok && alias != nil {
			val, ok = alias(dir, sub)
		}
		if !ok {
			return g
		}
		if depth > maxDepth || strings.HasPrefix(val, "!") {
			// Recursive, or a shell alias: cannot see what it runs.
			return gitCmd{sels: []analyzer.Selection{pending(dir)}, sub: sub, files: true, index: true}
		}
		lx := lex(val, "")
		if !lx.ok || len(lx.cmds) != 1 || len(lx.cmds[0].words) == 0 {
			return gitCmd{sels: []analyzer.Selection{pending(dir)}, sub: sub, files: true, index: true}
		}
		expanded := append(append([]word{}, lx.cmds[0].words...), rest...)
		inner := parseGit(expanded, dir, nil, alias, depth+1)
		if unknown == "" {
			return inner
		}
		g = inner
	}
	if len(g.sels) > 0 && unknown != "" {
		g.unresolved = "git " + g.sub + " in a repository chosen with " + unknown
		g.sels = nil
	}
	return g
}

// gitEffects reports whether a git subcommand may create or change
// working-tree files, or add content to the index, that a later command in
// the same line could stage or commit.
func gitEffects(sub string, rest []word) (files, index bool) {
	first := ""
	for _, w := range rest {
		if !strings.HasPrefix(w.text, "-") {
			first = w.text
			break
		}
	}
	switch sub {
	case "add", "stage":
		return false, intentToAdd(rest) // i-t-a entries let a later commit -a stage anything
	case "checkout", "switch":
		if createsBranchOnly(sub, rest) {
			return false, false
		}
		return true, true
	case "stash":
		if first == "list" || first == "show" {
			return false, false
		}
		return true, true
	case "worktree":
		if first == "list" || first == "prune" || first == "lock" || first == "unlock" {
			return false, false
		}
		return true, false
	case "lfs":
		switch first {
		case "ls-files", "status", "env", "version", "logs", "fsck", "locks", "pointer":
			return false, false
		}
		return true, true
	case "submodule":
		if first == "status" || first == "summary" {
			return false, false
		}
		return true, true
	case "am", "apply", "cherry-pick", "checkout-index", "merge", "pull", "read-tree",
		"rebase", "reset", "restore", "revert", "update-index":
		return true, true
	case "rm": // can untrack files or remove .gitignore/.gitattributes
		return true, false
	case "archive", "bundle", "clone", "format-patch", "init", "sparse-checkout":
		return true, false
	}
	return false, false
}

// createsBranchOnly reports whether checkout/switch only creates a branch at
// the current commit, leaving the working tree and index as they are.
func createsBranchOnly(sub string, rest []word) bool {
	created, positional := false, 0
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		if w.dynamic {
			return false
		}
		switch t := w.text; {
		case sub == "checkout" && (t == "-b" || t == "-B"),
			sub == "switch" && (t == "-c" || t == "-C" || t == "--create" || t == "--force-create"):
			if i+1 >= len(rest) {
				return false
			}
			i++
			created = true
		case sub == "switch" && (strings.HasPrefix(t, "--create=") || strings.HasPrefix(t, "--force-create=")):
			created = true
		case t == "-q" || t == "--quiet" || t == "-t" || t == "--track" || t == "--no-track":
		case strings.HasPrefix(t, "-"):
			return false
		default:
			positional++ // a start point or path: the tree may change
		}
	}
	return created && positional == 0
}

func intentToAdd(args []word) bool {
	for _, a := range args {
		t := a.text
		if t == "--" {
			return false
		}
		if strings.HasPrefix(t, "--") {
			name, _, _ := strings.Cut(t, "=")
			if expandLong(name, addLongOpts) == "--intent-to-add" {
				return true
			}
		} else if strings.HasPrefix(t, "-") && strings.ContainsRune(t[1:], 'N') {
			return true
		}
	}
	return false
}

var addLongOpts = []string{
	"--all", "--chmod", "--dry-run", "--edit", "--force", "--ignore-errors", "--ignore-missing",
	"--ignore-removal", "--intent-to-add", "--interactive", "--no-all", "--no-ignore-removal",
	"--patch", "--pathspec-file-nul", "--pathspec-from-file", "--refresh", "--renormalize",
	"--sparse", "--update", "--verbose",
}

func parseAdd(args []word, dir string) []analyzer.Selection {
	var specs []string
	all, update, force, dynamic, renormalize := false, false, false, false, false
	opts := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if opts && t == "--" {
			opts = false
			continue
		}
		if opts && strings.HasPrefix(t, "-") && t != "-" {
			if a.dynamic {
				dynamic = true
				continue
			}
			if strings.HasPrefix(t, "--") {
				name, _, hasValue := strings.Cut(t, "=")
				switch expandLong(name, addLongOpts) {
				case "--dry-run", "--intent-to-add", "--refresh":
					return nil // stages no content
				case "--all", "--no-ignore-removal", "--interactive", "--patch", "--edit":
					all = true
				case "--pathspec-from-file":
					dynamic = true // the list's contents are not known here
					if !hasValue {
						i++
					}
				case "--update":
					update = true
				case "--force":
					force = true
				case "--renormalize":
					renormalize = true
				}
				continue
			}
			for _, c := range t[1:] {
				switch c {
				case 'n', 'N':
					return nil
				case 'A', 'i', 'p', 'e':
					all = true
				case 'u':
					update = true
				case 'f':
					force = true
				}
			}
			continue
		}
		if a.dynamic {
			dynamic = true
			continue
		}
		specs = append(specs, t)
	}
	switch {
	case dynamic:
		sel := pending(dir)
		sel.Force = force
		return []analyzer.Selection{sel}
	case renormalize:
		// Re-applies filters and attributes to tracked files that look
		// unchanged, e.g. after `git lfs untrack`.
		if len(specs) == 0 {
			specs = []string{":/"}
		}
		return []analyzer.Selection{{Dir: dir, Kind: analyzer.KindPaths, Pathspecs: specs, Renormalize: true}}
	case all && len(specs) == 0:
		return []analyzer.Selection{{Dir: dir, Kind: analyzer.KindPaths, Pathspecs: []string{":/"}, Force: force}}
	case update:
		if len(specs) == 0 {
			specs = []string{":/"}
		}
		return []analyzer.Selection{{Dir: dir, Kind: analyzer.KindTracked, Pathspecs: specs}}
	case len(specs) == 0:
		return nil
	}
	return []analyzer.Selection{{Dir: dir, Kind: analyzer.KindPaths, Pathspecs: specs, Force: force}}
}

var commitLongOpts = []string{
	"--all", "--allow-empty", "--allow-empty-message", "--amend", "--author", "--branch",
	"--cleanup", "--date", "--dry-run", "--edit", "--file", "--fixup", "--gpg-sign",
	"--include", "--interactive", "--long", "--message", "--no-edit", "--no-gpg-sign",
	"--no-post-rewrite", "--no-status", "--no-verify", "--null", "--only", "--patch",
	"--pathspec-file-nul", "--pathspec-from-file", "--porcelain", "--quiet",
	"--reedit-message", "--reset-author", "--reuse-message", "--short", "--signoff",
	"--squash", "--status", "--template", "--trailer", "--untracked-files", "--verbose",
	"--verify",
}

// commitLongValue lists git commit long options that take a separate value.
var commitLongValue = map[string]bool{
	"--message": true, "--file": true, "--reuse-message": true, "--reedit-message": true,
	"--fixup": true, "--squash": true, "--author": true, "--date": true,
	"--template": true, "--cleanup": true, "--trailer": true, "--pathspec-from-file": true,
}

func parseCommit(args []word, dir string) []analyzer.Selection {
	var specs []string
	all, include, dynamic := false, false, false
	opts := true
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		if opts && t == "--" {
			opts = false
			continue
		}
		if opts && strings.HasPrefix(t, "-") && t != "-" {
			if strings.HasPrefix(t, "--") {
				name, _, hasValue := strings.Cut(t, "=")
				name = expandLong(name, commitLongOpts)
				switch name {
				case "--dry-run":
					return nil
				case "--all", "--interactive", "--patch":
					all = true
				case "--include":
					include = true
				case "--pathspec-from-file":
					dynamic = true
				}
				if !hasValue && commitLongValue[name] {
					i++
				}
				continue
			}
			short := t[1:]
			for j, c := range short {
				if strings.ContainsRune("mFCct", c) { // takes a value
					if j == len(short)-1 {
						i++
					}
					break
				}
				if c == 'S' || c == 'u' { // optional value, always attached
					break
				}
				switch c {
				case 'a', 'p':
					all = true
				case 'i':
					include = true
				}
			}
			continue
		}
		if a.dynamic {
			dynamic = true
			continue
		}
		specs = append(specs, t)
	}
	staged := analyzer.Selection{Dir: dir, Kind: analyzer.KindStaged}
	switch {
	case dynamic:
		return []analyzer.Selection{pending(dir)}
	case all:
		return []analyzer.Selection{staged, {Dir: dir, Kind: analyzer.KindTracked, Pathspecs: []string{":/"}}}
	case len(specs) > 0 && include:
		return []analyzer.Selection{staged, {Dir: dir, Kind: analyzer.KindTracked, Pathspecs: specs}}
	case len(specs) > 0:
		// git commit <paths> records only those paths, from the working tree.
		return []analyzer.Selection{{Dir: dir, Kind: analyzer.KindTracked, Pathspecs: specs}}
	}
	return []analyzer.Selection{staged}
}

// expandLong resolves an unambiguous abbreviation of a long option, as git's
// option parser does.
func expandLong(name string, opts []string) string {
	match := ""
	for _, o := range opts {
		if o == name {
			return o
		}
		if len(name) > 2 && strings.HasPrefix(o, name) {
			if match != "" {
				return name // ambiguous
			}
			match = o
		}
	}
	if match == "" {
		return name
	}
	return match
}

// stripPrefix drops leading keywords and variable assignments, returning the
// remaining words and the assignments.
func stripPrefix(words []word) ([]word, []word) {
	var env []word
	for len(words) > 0 {
		w := words[0]
		switch {
		case envRe.MatchString(w.text):
			env = append(env, w)
		case !w.dynamic && prefixWords[w.text]:
		default:
			return words, env
		}
		words = words[1:]
	}
	return words, env
}

// shellScript returns the script a shell invocation runs: the -c argument,
// or its stdin (heredoc/here-string) when it reads commands from stdin.
// expands reports that the outer shell expands the text first. ok is false
// for a script file, a -c string that cannot be known, or stdin from a pipe.
func shellScript(args []word, stdin []stdinText) (script string, ok, expands bool) {
	for i := 0; i < len(args); i++ {
		t := args[i].text
		if args[i].dynamic {
			return "", false, false
		}
		if t == "-" || t == "--" {
			break // end of options: script from stdin
		}
		if !strings.HasPrefix(t, "-") {
			return "", false, false // a script file
		}
		if !strings.HasPrefix(t, "--") && strings.Contains(t, "c") {
			if i+1 >= len(args) || args[i+1].dynamic {
				return "", false, false
			}
			return args[i+1].text, true, false
		}
		if t == "-o" || t == "+o" || t == "--rcfile" || t == "--init-file" {
			i++
		}
	}
	if len(stdin) == 0 {
		return "", false, false
	}
	texts := make([]string, len(stdin))
	for k, in := range stdin {
		texts[k] = in.text
		expands = expands || (in.expands && strings.ContainsAny(in.text, "$`\\"))
	}
	return strings.Join(texts, "\n"), true, expands
}

// readsStdin reports whether a shell invocation reads its script from stdin
// (no -c and no script file), as in `echo 'git add .' | bash`.
func readsStdin(args []word) bool {
	for i := 0; i < len(args); i++ {
		t := args[i].text
		switch {
		case args[i].dynamic:
			return false
		case t == "-" || t == "--" || t == "-s":
			return true
		case !strings.HasPrefix(t, "-"):
			return false
		case !strings.HasPrefix(t, "--") && strings.Contains(t, "c"):
			return false
		case t == "-o" || t == "+o" || t == "--rcfile" || t == "--init-file":
			i++
		}
	}
	return true
}

// cdTarget returns the directory a cd/pushd moves to. ok is false when it
// cannot be known statically; absolute reports that it does not depend on
// the starting directory; toplevel reports cd "$(git rev-parse --show-toplevel)".
func cdTarget(dir string, args []word, home string) (target string, ok, absolute, toplevel bool) {
	for len(args) > 0 && strings.HasPrefix(args[0].text, "-") && args[0].text != "-" {
		args = args[1:] // -L, -P, -e, -@
	}
	if len(args) == 0 {
		if home == "" {
			return dir, false, false, false
		}
		return home, true, true, false
	}
	a := args[0]
	if a.dynamic {
		if strings.Join(strings.Fields(a.subst), " ") == "git rev-parse --show-toplevel" {
			return dir, true, false, true
		}
		return dir, false, false, false
	}
	if a.text == "-" {
		return dir, false, false, false
	}
	return resolve(dir, a.text), true, filepath.IsAbs(a.text), false
}

// mentionsStaging reports whether words run a git staging command: a "git"
// word followed by add/stage/commit, or a single argument holding such a
// command line (as in xargs sh -c 'git add "$1"').
func mentionsStaging(words []word) bool {
	if stagingTokens(words) {
		return true
	}
	for _, w := range words {
		if strings.ContainsAny(w.text, " \t\n") && gitRe.MatchString(w.text) && verbRe.MatchString(w.text) {
			return true
		}
	}
	return false
}

// stagingTokens reports a "git" word followed later by add/stage/commit.
func stagingTokens(words []word) bool {
	sawGit := false
	for _, w := range words {
		if sawGit && isVerb(w.text) {
			return true
		}
		if filepath.Base(w.text) == "git" {
			sawGit = true
		}
	}
	return false
}

// hasForce reports a force option after an add/stage word (git add -f).
func hasForce(words []word) bool {
	sawAdd := false
	for _, w := range words {
		t := w.text
		switch {
		case t == "add" || t == "stage":
			sawAdd = true
		case !sawAdd:
		case expandLong(t, addLongOpts) == "--force":
			return true
		case strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.ContainsRune(t, 'f'):
			return true
		}
	}
	return false
}

func stagingVerb(words []word) bool {
	for _, w := range words {
		if !w.dynamic && isVerb(w.text) {
			return true
		}
	}
	return false
}

func isVerb(s string) bool {
	return s == "add" || s == "stage" || s == "commit"
}

// commandIsSafe reports whether a non-git command leaves working-tree files
// (and what git add would pick up) unchanged.
func commandIsSafe(base string, words []word) bool {
	args := words[1:]
	switch base {
	case "sed":
		return !hasOption(args, "-i", "--in-place")
	case "sort":
		return !hasOption(args, "-o", "--output")
	case "gofmt":
		return !hasOption(args, "-w")
	case "rm": // removing .gitignore or .gitattributes changes what git add picks up
		for _, a := range args {
			if a.dynamic || gitMetaFile(a.text) {
				return false
			}
		}
		return true
	case "go":
		for _, a := range args {
			if strings.HasPrefix(a.text, "-") {
				continue
			}
			switch a.text {
			case "test", "vet", "version", "env", "list", "doc":
				return !hasOption(args, "-update", "-o")
			}
			return false
		}
		return true
	}
	return safeCommands[base]
}

// hasOption reports whether args contain one of opts (as a separate word, a
// "--opt=value" form, or inside a short option cluster for one-letter options).
func hasOption(args []word, opts ...string) bool {
	for _, a := range args {
		t := a.text
		for _, o := range opts {
			if t == o || strings.HasPrefix(t, o+"=") {
				return true
			}
			if len(o) == 2 && strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.ContainsRune(t[1:], rune(o[1])) {
				return true
			}
		}
	}
	return false
}

func gitMetaFile(p string) bool {
	switch filepath.Base(p) {
	case ".gitignore", ".gitattributes", ".gitmodules":
		return true
	}
	return strings.Contains(p, ".git/info/")
}

// writesInto reports whether output redirections may create or change files
// that git add could pick up: anything except temp directories outside the
// current one (redirects to /dev/... are never recorded).
func writesInto(dir string, targets []word) bool {
	for _, t := range targets {
		if t.dynamic || !filepath.IsAbs(t.text) {
			return true
		}
		p := filepath.Clean(t.text)
		if within(p, dir) || !inTempDir(p) {
			return true
		}
	}
	return false
}

func inTempDir(p string) bool {
	for _, d := range []string{os.TempDir(), "/tmp", "/private/tmp", "/var/folders", "/private/var/folders"} {
		if within(p, filepath.Clean(d)) {
			return true
		}
	}
	return false
}

func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

func findRunsCommands(words []word) bool {
	for _, w := range words {
		switch w.text {
		case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-fprint", "-fprint0", "-fprintf", "-fls":
			return true
		}
	}
	return false
}

func resolve(dir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(dir, p)
}
