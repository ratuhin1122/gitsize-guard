package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

type decision struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

// hook runs `gitsize hook` (warn at 1 MB, fail at 2 MB) on a payload and
// returns the decision ("" when it prints nothing) and its reason.
func hook(t *testing.T, payload map[string]any) (string, string) {
	t.Helper()
	in, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "--warning-mb", "1", "--failure-mb", "2"}, bytes.NewReader(in), &stdout, &stderr); code != 0 {
		t.Fatalf("hook exited %d; stderr: %s", code, stderr.String())
	}
	if stdout.Len() == 0 {
		return "", ""
	}
	var d decision
	if err := json.Unmarshal(stdout.Bytes(), &d); err != nil {
		t.Fatalf("hook printed invalid JSON %q: %v", stdout.String(), err)
	}
	if d.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q", d.HookSpecificOutput.HookEventName)
	}
	return d.HookSpecificOutput.PermissionDecision, d.HookSpecificOutput.PermissionDecisionReason
}

func bash(cwd, command string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command},
		"cwd":             cwd,
	}
}

func write(cwd, path, content string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Write",
		"tool_input":      map[string]any{"file_path": path, "content": content},
		"cwd":             cwd,
	}
}

func TestHookBash(t *testing.T) {
	clearThresholdEnv(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "big.bin"), randomBytes(t, 3*mib))
	writeFile(t, filepath.Join(repo, "medium.bin"), randomBytes(t, 3*mib/2))
	writeFile(t, filepath.Join(repo, "small.txt"), []byte("hello\n"))
	writeFile(t, filepath.Join(repo, "sub", "model.bin"), randomBytes(t, 3*mib))
	sub := filepath.Join(repo, "sub")

	tests := []struct {
		name       string
		cwd, cmd   string
		want       string
		wantReason string
	}{
		{"small file", repo, "git add small.txt", "", ""},
		{"large file", repo, "git add big.bin", "deny", "big.bin"},
		{"medium file asks", repo, "git add medium.bin", "ask", "warning threshold"},
		{"dot", repo, "git add .", "deny", "big.bin"},
		{"all", repo, "git add -A && git commit -m wip", "deny", "sub/model.bin"},
		{"directory", repo, "git add sub/", "deny", "sub/model.bin"},
		{"second path", repo, "git add small.txt big.bin", "deny", "big.bin"},
		{"cd then add", repo, "cd sub && git add .", "deny", "sub/model.bin"},
		{"-C", repo, "git -C sub add model.bin", "deny", "sub/model.bin"},
		{"relative to cwd", sub, "git add model.bin", "deny", "sub/model.bin"},
		{"indirect asks", repo, "find . -name '*.bin' | xargs git add", "ask", "can't tell exactly"},
		{"read-only command mentioning git add", repo, `grep -rn "git add" . ; echo "git commit"`, "", ""},
		{"created in the same command", repo, "head -c 3145728 /dev/zero > new.bin && git add new.bin && git commit -m new", "deny", "separate command"},
		{"commit after a build is fine", repo, "make build && git commit -m 'staged only'", "", ""},
		{"unresolvable repository asks", repo, "git --git-dir=/x/repo.git add big.bin", "ask", "--git-dir"},
		{"commit with nothing large staged", repo, "git commit -m 'small change'", "", ""},
		{"read-only git", repo, "git status && git diff", "", ""},
		{"not git", repo, "ls -la", "", ""},
		{"outside a repository", t.TempDir(), "git add .", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := hook(t, bash(tc.cwd, tc.cmd))
			if got != tc.want {
				t.Fatalf("decision = %q, want %q (reason: %s)", got, tc.want, reason)
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason %q does not mention %q", reason, tc.wantReason)
			}
		})
	}

	t.Run("retry is still denied", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if got, _ := hook(t, bash(repo, "git add big.bin")); got != "deny" {
				t.Fatalf("attempt %d: decision = %q, want deny", i+1, got)
			}
		}
	})

	t.Run("deny gives actionable advice", func(t *testing.T) {
		_, reason := hook(t, bash(repo, "git add big.bin"))
		for _, want := range []string{"blocked", "3.0 MB", "binary", "git lfs track"} {
			if !strings.Contains(reason, want) {
				t.Errorf("reason %q does not mention %q", reason, want)
			}
		}
	})
}

func TestHookCommit(t *testing.T) {
	clearThresholdEnv(t)
	repo := newRepo(t)

	claudeCommit := "git commit -m \"$(cat <<'EOF'\nAdd model weights\n\nCo-Authored-By: Claude <noreply@anthropic.com>\nEOF\n)\""

	writeFile(t, filepath.Join(repo, "untracked.bin"), randomBytes(t, 3*mib))
	if got, reason := hook(t, bash(repo, claudeCommit)); got != "" {
		t.Errorf("commit with only an untracked large file: decision %q (%s), want none", got, reason)
	}

	git(t, repo, "add", "untracked.bin") // e.g. staged by the user outside Claude Code
	if got, reason := hook(t, bash(repo, claudeCommit)); got != "deny" || !strings.Contains(reason, "untracked.bin: 3.0 MB (staged") {
		t.Errorf("commit of a staged large file: decision %q (%s), want deny naming it", got, reason)
	}
	git(t, repo, "reset", "-q", "untracked.bin")

	writeFile(t, filepath.Join(repo, "README.md"), randomBytes(t, 3*mib))
	if got, _ := hook(t, bash(repo, "git commit -am 'update readme'")); got != "deny" {
		t.Errorf("commit -a with a large tracked change: decision %q, want deny", got)
	}
	if got, _ := hook(t, bash(repo, "git commit -m 'staged only'")); got != "" {
		t.Errorf("commit without -a ignores unstaged changes: decision %q, want none", got)
	}
}

func TestHookWrite(t *testing.T) {
	clearThresholdEnv(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitignore"), []byte("build/\n"))
	big := strings.Repeat("a", 3*mib)

	tests := []struct {
		name, path, content, want string
	}{
		{"small", filepath.Join(repo, "notes.txt"), "hi", ""},
		{"large new file in new directory", filepath.Join(repo, "data", "nested", "dump.sql"), big, "deny"},
		{"relative path", "dump.sql", big, "deny"},
		{"medium", filepath.Join(repo, "m.txt"), strings.Repeat("a", 3*mib/2), "ask"},
		{"ignored", filepath.Join(repo, "build", "out.txt"), big, ""},
		{"outside a repository", filepath.Join(t.TempDir(), "x.txt"), big, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, reason := hook(t, write(repo, tc.path, tc.content)); got != tc.want {
				t.Errorf("decision = %q (%s), want %q", got, reason, tc.want)
			}
		})
	}
}

func TestHookIgnoresOtherInput(t *testing.T) {
	clearThresholdEnv(t)
	repo := newRepo(t)

	if got, _ := hook(t, map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": "x"}, "cwd": repo}); got != "" {
		t.Errorf("Read tool: decision %q, want none", got)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook"}, strings.NewReader("not json"), &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Errorf("invalid payload: exit %d, stdout %q; want 0 and no decision", code, stdout.String())
	}
}

func TestHookRound2Decisions(t *testing.T) {
	clearThresholdEnv(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "big.bin"), randomBytes(t, 3*mib))
	writeFile(t, filepath.Join(repo, "medium.bin"), randomBytes(t, 3*mib/2))
	writeFile(t, filepath.Join(repo, "small.txt"), []byte("hello\n"))

	tests := []struct {
		name, cmd, want, wantReason string
	}{
		{"branch then commit", "git checkout -b feat/x && git add small.txt && git commit -m x", "", ""},
		{"index change then commit", "git stash pop && git commit -m restore", "deny", "separate command"},
		{"estimate does not soften an exact deny", `git add big.bin && git add "$F"`, "deny", "blocked"},
		{"notes ride along on asks", "git add medium.bin && git --git-dir=/x/r.git add y", "ask", "Not checked"},
		{"toplevel cd is exact", `cd "$(git rev-parse --show-toplevel)" && git add big.bin`, "deny", "big.bin"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := hook(t, bash(repo, tc.cmd))
			if got != tc.want || !strings.Contains(reason, tc.wantReason) {
				t.Errorf("decision = %q (%s), want %q mentioning %q", got, reason, tc.want, tc.wantReason)
			}
		})
	}
}

func TestHookWriteHonoursLFS(t *testing.T) {
	clearThresholdEnv(t)
	repo := newRepo(t)
	git(t, repo, "config", "filter.lfs.clean", "cat")
	git(t, repo, "config", "filter.other.clean", "cat")
	writeFile(t, filepath.Join(repo, ".gitattributes"), []byte("*.psd filter=lfs\n*.enc filter=other\n"))
	big := strings.Repeat("a", 3*mib)
	for _, tc := range []struct{ path, want string }{
		{"art.psd", ""},       // stored as an LFS pointer
		{"secret.enc", "ask"}, // filtered: size after the filter unknown
		{"plain.txt", "deny"},
	} {
		if got, reason := hook(t, write(repo, filepath.Join(repo, tc.path), big)); got != tc.want {
			t.Errorf("Write %s: decision %q (%s), want %q", tc.path, got, reason, tc.want)
		}
	}
}

func TestHookConfigErrorKeepsValidSettings(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "medium.bin"), randomBytes(t, 3*mib/2))
	t.Setenv("GITSIZE_WARNING_MB", "1")
	t.Setenv("GITSIZE_FAILURE_MB", "2MB") // invalid: the default failure applies, the warning is kept
	in := `{"tool_name":"Bash","tool_input":{"command":"git add medium.bin"},"cwd":"` + repo + `"}`
	var stdout, stderr bytes.Buffer
	run([]string{"hook"}, strings.NewReader(in), &stdout, &stderr)
	out := stdout.String()
	if !strings.Contains(out, `"ask"`) || !strings.Contains(out, "warning threshold 1.0 MB") || !strings.Contains(out, "configuration error") {
		t.Errorf("want an ask at the kept 1 MB warning threshold with a configuration note; got %s", out)
	}
}
