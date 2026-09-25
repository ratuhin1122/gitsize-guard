package shellcmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

const (
	cwd  = "/work/repo"
	home = "/home/me"
)

func paths(dir string, specs ...string) analyzer.Selection {
	return analyzer.Selection{Dir: dir, Kind: analyzer.KindPaths, Pathspecs: specs}
}

func tracked(dir string, specs ...string) analyzer.Selection {
	return analyzer.Selection{Dir: dir, Kind: analyzer.KindTracked, Pathspecs: specs}
}

func staged(dir string) analyzer.Selection {
	return analyzer.Selection{Dir: dir, Kind: analyzer.KindStaged}
}

func TestSelections(t *testing.T) {
	claudeCommit := "git add model.bin && git commit -m \"$(cat <<'EOF'\nAdd model (don't \"quote\" me)\n\nCo-Authored-By: x <y@z>\nEOF\n)\""

	tests := []struct {
		name string
		cmd  string
		want []analyzer.Selection
	}{
		{"not git", "ls -la && echo add commit", nil},
		{"read-only git", "git status && git log --oneline -5", nil},
		{"single file", "git add model.bin", []analyzer.Selection{paths(cwd, "model.bin")}},
		{"extra spaces", "git   add    model.bin", []analyzer.Selection{paths(cwd, "model.bin")}},
		{"stage alias", "git stage a.bin", []analyzer.Selection{paths(cwd, "a.bin")}},
		{"multiple files", "git add README.md model.bin", []analyzer.Selection{paths(cwd, "README.md", "model.bin")}},
		{"dot", "git add .", []analyzer.Selection{paths(cwd, ".")}},
		{"glob", "git add '*.bin'", []analyzer.Selection{paths(cwd, "*.bin")}},
		{"directory", "git add assets/", []analyzer.Selection{paths(cwd, "assets/")}},
		{"all", "git add -A", []analyzer.Selection{paths(cwd, ":/")}},
		{"all long", "git add --all", []analyzer.Selection{paths(cwd, ":/")}},
		{"abbreviated long option", "git add --al", []analyzer.Selection{paths(cwd, ":/")}},
		{"force", "git add -f build/out.bin", []analyzer.Selection{{Dir: cwd, Kind: analyzer.KindPaths, Pathspecs: []string{"build/out.bin"}, Force: true}}},
		{"combined short flags", "git add -Af", []analyzer.Selection{{Dir: cwd, Kind: analyzer.KindPaths, Pathspecs: []string{":/"}, Force: true}}},
		{"update", "git add -u", []analyzer.Selection{tracked(cwd, ":/")}},
		{"double dash", "git add -- -weird-name", []analyzer.Selection{paths(cwd, "-weird-name")}},
		{"dry run", "git add -n .", nil},
		{"intent to add", "git add -N big.bin", nil},
		{"no paths", "git add", nil},
		{"pathspec from file", "git add --pathspec-from-file list.txt", []analyzer.Selection{pending(cwd)}},
		{"pathspec from stdin", "ls *.bin | git add --pathspec-from-file=-", []analyzer.Selection{pending(cwd)}},
		{"-C", "git -C sub add x.bin", []analyzer.Selection{paths(cwd+"/sub", "x.bin")}},
		{"-c config", "git -c core.autocrlf=false add x.bin", []analyzer.Selection{paths(cwd, "x.bin")}},
		{"--work-tree", "git --git-dir=/r/.git --work-tree=/r add -A", []analyzer.Selection{paths("/r", ":/")}},
		{"--git-dir of a .git directory", "git --git-dir /r/.git add y", []analyzer.Selection{paths("/r", "y")}},
		{"GIT_WORK_TREE assignment", "GIT_DIR=/r/.git GIT_WORK_TREE=/r git add y", []analyzer.Selection{paths("/r", "y")}},
		{"cd first", "cd sub && git add .", []analyzer.Selection{paths(cwd+"/sub", ".")}},
		{"cd absolute", "cd /other && git add .", []analyzer.Selection{paths("/other", ".")}},
		{"cd home", "cd ~/proj; git add x", []analyzer.Selection{paths(home+"/proj", "x")}},
		{"subshell cd is scoped", "(cd sub && git add a) && git add b", []analyzer.Selection{paths(cwd+"/sub", "a"), paths(cwd, "b")}},
		{"pushd popd", "pushd sub && git add a && popd && git add b", []analyzer.Selection{paths(cwd+"/sub", "a"), paths(cwd, "b")}},
		{"env prefix", "GIT_TRACE=1 git add x", []analyzer.Selection{paths(cwd, "x")}},
		{"absolute git", "/usr/bin/git add x", []analyzer.Selection{paths(cwd, "x")}},
		{"redirects", "git add x 2>/dev/null >out.log", []analyzer.Selection{paths(cwd, "x")}},
		{"stderr dup", "git add x 2>&1 | tail -1", []analyzer.Selection{paths(cwd, "x")}},
		{"comment", "git add x # and y", []analyzer.Selection{paths(cwd, "x")}},
		{"commit", "git commit -m 'msg with add'", []analyzer.Selection{staged(cwd)}},
		{"commit -am", "git commit -am wip", []analyzer.Selection{staged(cwd), tracked(cwd, ":/")}},
		{"commit --all", "git commit --all --message=wip", []analyzer.Selection{staged(cwd), tracked(cwd, ":/")}},
		{"commit paths records only those paths", "git commit -m msg -- a.txt b.bin", []analyzer.Selection{tracked(cwd, "a.txt", "b.bin")}},
		{"commit --include paths", "git commit -i -m msg a.txt", []analyzer.Selection{staged(cwd), tracked(cwd, "a.txt")}},
		{"commit option values", "git commit --author 'A <a@b>' -F msg.txt -S", []analyzer.Selection{staged(cwd)}},
		{"commit dry run", "git commit --dry-run", nil},
		{"claude-style commit", claudeCommit, []analyzer.Selection{paths(cwd, "model.bin"), staged(cwd)}},
		{"heredoc body is data", "git commit -F - <<EOF\ngit add everything.bin\nEOF\ngit status", []analyzer.Selection{staged(cwd)}},
		{"variable path", `git add "$FILE"`, []analyzer.Selection{pending(cwd)}},
		{"brace expansion", "git add shard-{1,2,3}.bin", []analyzer.Selection{pending(cwd)}},
		{"brace sequence", "git add part{1..4}.bin", []analyzer.Selection{pending(cwd)}},
		{"loop", "for f in *.bin; do git add \"$f\"; done", []analyzer.Selection{pending(cwd)}},
		{"xargs", "find . -name '*.bin' | xargs git add", []analyzer.Selection{pending(cwd)}},
		{"find -exec", "find . -name '*.bin' -exec git add {} +", []analyzer.Selection{pending(cwd)}},
		{"sh -c is parsed", `sh -c "git add . && git commit -m x"`, []analyzer.Selection{paths(cwd, "."), staged(cwd)}},
		{"bash -lc is parsed", `bash -lc 'git add big.bin'`, []analyzer.Selection{paths(cwd, "big.bin")}},
		{"script on stdin", "bash <<'EOF'\ngit add big.bin\nEOF", []analyzer.Selection{paths(cwd, "big.bin")}},
		{"here-string script", `sh <<< "git add big.bin"`, []analyzer.Selection{paths(cwd, "big.bin")}},
		{"script file is opaque", "bash release.sh", nil},
		{"indirect after cd", "cd /other && xargs git add < list.txt", []analyzer.Selection{pending("/other")}},
		{"dynamic git binary", `"$GIT" add big.bin`, []analyzer.Selection{pending(cwd)}},
		{"git from substitution", "$(which git) commit -am x", []analyzer.Selection{pending(cwd)}},
		{"substitution with git", "echo $(git add big.bin)", []analyzer.Selection{paths(cwd, "big.bin")}},
		{"backticks with git", "echo `git add big.bin`", []analyzer.Selection{paths(cwd, "big.bin")}},
		{"unquoted heredoc runs substitutions", "cat <<EOF\n$(git add big.bin)\nEOF", []analyzer.Selection{paths(cwd, "big.bin")}},
		{"quoted heredoc does not", "cat <<'EOF'\n$(git add big.bin)\nEOF", nil},
		{"arithmetic shift is not a heredoc", "(( n = 1 << 4 ))\ngit add big.bin", []analyzer.Selection{paths(cwd, "big.bin")}},
		{"ANSI-C quoting", `echo $'don\'t' && git add big.bin`, []analyzer.Selection{paths(cwd, "big.bin")}},
		{"comment with quote in substitution", "x=$(\n  # it's fine\n  echo hi\n) && git add big.bin", []analyzer.Selection{paths(cwd, "big.bin")}},
		{"unicode space is not a separator", "git add Screen Recording.mov", []analyzer.Selection{paths(cwd, "Screen Recording.mov")}},
		{"unterminated quote", "git add 'oops", []analyzer.Selection{paths(cwd, "oops"), pending(cwd)}},
		{"line continuation", "git add \\\n  big.bin", []analyzer.Selection{paths(cwd, "big.bin")}},
		{"grep mentioning git add", `grep -rn "git add" docs/ && rg 'git (add|commit)'`, nil},
		{"echo mentioning git commit", `echo "next: git add . && git commit"`, nil},
		{"gh pr body", "gh pr create --title 'Add git commit hook' --body \"$(cat <<'EOF'\nRun git add -A then git commit.\nEOF\n)\"", nil},
		{"read-only git with variable -C", `for d in */; do git -C "$d" log -1 --oneline; done`, nil},
		{"renormalize", "git add --renormalize .", []analyzer.Selection{{Dir: cwd, Kind: analyzer.KindPaths, Pathspecs: []string{"."}, Renormalize: true}}},
		{"forced variable path", `git add -f "$F"`, []analyzer.Selection{{Dir: cwd, Kind: analyzer.KindPending, Force: true}}},
		{"forced xargs", "ls *.bin | xargs git add -f", []analyzer.Selection{{Dir: cwd, Kind: analyzer.KindPending, Force: true}}},
		{"script piped to a shell", "echo 'git add big.bin' | bash", []analyzer.Selection{pending(cwd)}},
		{"unlisted wrapper", "caffeinate git add big.bin", []analyzer.Selection{pending(cwd)}},
		{"env with options", "env -u FOO git add big.bin", []analyzer.Selection{pending(cwd)}},
		{"cd to the toplevel", `cd "$(git rev-parse --show-toplevel)" && git add src/a.go docs`, []analyzer.Selection{paths(cwd, ":/src/a.go", ":/docs")}},
		{"cd to the toplevel, then dot", "cd $(git rev-parse --show-toplevel) && git add .", []analyzer.Selection{paths(cwd, ":/")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Selections(tc.cmd, cwd, home, nil)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Selections(%q)\n got: %+v\nwant: %+v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestParseUnseen(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"dd if=/dev/zero of=model.bin bs=1M count=150 && git add model.bin && git commit -m m", true},
		{"head -c 157286400 /dev/zero > model.bin && git add model.bin", true},
		{"cp /tmp/big.bin . && git add big.bin", true},
		{"curl -o data.bin https://example.com/x && git add .", true},
		{"npm run build && git add -A && git commit -m build", true},
		{"git init && git add -A && git commit -m init", true},
		{"git stash pop && git add -A", true},
		{"sed -i 's/a/b/' f.txt && git add f.txt", true},
		{"echo hi > notes.txt && git add notes.txt", true},
		{"git add -A && git commit -m x", false},
		{"cd sub && mkdir -p out && touch out/.keep && git add out", false},
		{"rm big.bin && git add -A", false},
		{"git status && echo done > /dev/null && git add x", false},
		{"sed 's/a/b/' f.txt && git add f.txt", false},
		{"npm run build", false},
		{"npm run build && git commit -m x", false}, // commits only what is already staged
		{"git add x && npm test", false},
		// Commands that change neither files nor the index.
		{"git checkout -b feat/x && git add -A && git commit -m x", false},
		{"git switch -c feat/y && git add -A", false},
		{"git stash list && git add -A", false},
		{"cd sub || exit 1; git add .", false},
		{"shasum -a 256 m.bin && git add m.bin", false},
		{`git ls-files -m | while read f; do echo "$f"; done; git add a.bin`, false},
		{"files=(a.bin b.bin); git add a.bin", false},
		{"cat > /tmp/msg.txt <<'EOF'\nfix\nEOF\ngit add -A && git commit -F /tmp/msg.txt", false},
		{"go test ./... && git add -A && git commit -m x", false},
		{"git mv a.txt b.txt && git commit -m rename", false},
		{"git rm old.txt && git commit -m remove", false},
		// Commands that change the index before a commit.
		{"git apply --cached big.patch && git commit -m x", true},
		{"git stash pop --index && git commit -m restore", true},
		{"git add -N big.bin && git commit -a -m x", true},
		{"git merge --squash feature && git commit -m squash", true},
		{"git checkout other -- big.bin && git commit -m x", true},
		{"git checkout -b x origin/main && git add -A", true},
		// Writes the parser used to miss.
		{"{ head -c 3000000 /dev/zero; } > m.bin && git add m.bin", true},
		{"(head -c 3000000 /dev/zero) > m.bin && git add m.bin", true},
		{"head -c 3000000 /dev/zero &> m.bin && git add m.bin", true},
		{`x="$(head -c 3000000 /dev/zero > big.bin)"; git add big.bin`, true},
		{"git rm .gitattributes && git add -A", true},
		{"rm .gitignore && git add -A", true},
		{"git rm --cached m.psd && git add m.psd", true},
		{"git archive -o out.zip HEAD && git add out.zip", true},
		{"sort -o f.txt f.txt && git add f.txt", true},
		{"go build -o bin/tool . && git add -f bin/tool", true},
	}
	for _, tc := range tests {
		if got := Parse(tc.cmd, cwd, home, nil).Unseen; got != tc.want {
			t.Errorf("Parse(%q).Unseen = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestParseUnresolved(t *testing.T) {
	tests := []struct {
		cmd  string
		want string // substring of an Unresolved entry; "" for none
	}{
		{`cd "$DIR" && git add .`, "cd"},
		{`cd "$(git rev-parse --show-toplevel)" && git status && echo "nothing to commit"`, ""},
		{`cd "$DIR" && cd /abs && git add .`, ""},
		{"git --git-dir=/x/repo.git add y", "--git-dir"},
		{`git -C "$REPO" add big.bin`, "-C"},
		{`git -C "$REPO" status`, ""},
		{"GIT_INDEX_FILE=/tmp/idx git add big.bin", "GIT_INDEX_FILE"},
		{"git rebase -x 'git add -A && git commit --amend --no-edit' HEAD~3", "rebase"},
		{"git rebase -x 'make test' HEAD~3", ""},
		{"git submodule foreach git add -A", "submodule"},
		{"git --work-tree=/b add big.bin", "--work-tree"},
		{"GIT_WORK_TREE=/b git add big.bin", "--work-tree"},
		{"git --git-dir=/r/.git --work-tree=/r add -A", ""},
		{`cd "$(git rev-parse --show-toplevel)" && cd sub && git add x`, "cd"},
	}
	for _, tc := range tests {
		p := Parse(tc.cmd, cwd, home, nil)
		got := strings.Join(p.Unresolved, "; ")
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("Parse(%q).Unresolved = %q, want %q", tc.cmd, got, tc.want)
		}
		if tc.want != "" && len(p.Selections) > 0 {
			t.Errorf("Parse(%q): unresolved command still produced selections %+v", tc.cmd, p.Selections)
		}
	}
}

func TestParseUnquotedHeredocScript(t *testing.T) {
	// The outer shell expands $F before bash reads the script, so what it
	// stages can't be known; quoted heredocs are taken literally.
	for _, tc := range []struct {
		cmd     string
		pending bool
	}{
		{"bash <<EOF\ngit add \\$F\nEOF", true},
		{"bash <<'EOF'\ngit add big.bin\nEOF", false},
	} {
		got := false
		for _, s := range Selections(tc.cmd, cwd, home, nil) {
			got = got || s.Kind == analyzer.KindPending
		}
		if got != tc.pending {
			t.Errorf("Selections(%q): pending=%v, want %v", tc.cmd, got, tc.pending)
		}
	}
}

func TestSelectionsAliases(t *testing.T) {
	aliases := map[string]string{
		"ci":   "commit -v",
		"aa":   "add --all",
		"save": "!git add -A && git commit -m save",
		"loop": "loop",
	}
	lookup := func(dir, name string) (string, bool) {
		v, ok := aliases[name]
		return v, ok
	}

	tests := []struct {
		cmd  string
		want []analyzer.Selection
	}{
		{"git ci -m x", []analyzer.Selection{staged(cwd)}},
		{"git aa", []analyzer.Selection{paths(cwd, ":/")}},
		{"git save", []analyzer.Selection{pending(cwd)}},
		{"git loop", []analyzer.Selection{pending(cwd)}},
		{"git unknown-thing", nil},
		{"git -c alias.ship='add big.bin' ship", []analyzer.Selection{paths(cwd, "big.bin")}},
	}
	for _, tc := range tests {
		t.Run(tc.cmd, func(t *testing.T) {
			got := Selections(tc.cmd, cwd, home, lookup)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Selections(%q)\n got: %+v\nwant: %+v", tc.cmd, got, tc.want)
			}
		})
	}
}

func TestLexWords(t *testing.T) {
	lx := lex(`a "b c" 'd e' f\ g "h\"i" $V "x$(y)z" $'p\tq'`, "")
	if !lx.ok || len(lx.cmds) != 1 {
		t.Fatalf("lex failed: ok=%v cmds=%d", lx.ok, len(lx.cmds))
	}
	var texts []string
	var dyn []bool
	for _, w := range lx.cmds[0].words {
		texts = append(texts, w.text)
		dyn = append(dyn, w.dynamic)
	}
	wantTexts := []string{"a", "b c", "d e", "f g", `h"i`, "$V", "x$z", "p\tq"}
	wantDyn := []bool{false, false, false, false, false, true, true, false}
	if !reflect.DeepEqual(texts, wantTexts) || !reflect.DeepEqual(dyn, wantDyn) {
		t.Errorf("words = %q dynamic=%v, want %q dynamic=%v", texts, dyn, wantTexts, wantDyn)
	}
	if len(lx.subs) != 1 || lx.subs[0] != "y" {
		t.Errorf("subs = %q, want [y]", lx.subs)
	}
}

// FuzzParse checks that no input makes the parser panic or hang.
func FuzzParse(f *testing.F) {
	for _, s := range []string{
		"git add .", "git commit -m \"$(cat <<'EOF'\nx\nEOF\n)\"", "((1<<2))", "$'\\'", "a <<", "$((", "`", "(((",
		"bash <<<", "git -c", "git --git-dir", "cd", "x=$( # ' \n)", "{a,b}", "git add \\",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		Parse(s, cwd, home, func(string, string) (string, bool) { return "add " + s, true })
	})
}
