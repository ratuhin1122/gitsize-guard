package main

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exercise hooks/pretooluse-gitsize.sh, the shim Claude Code runs.

func repoRootDir(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func runShim(t *testing.T, shim, dir string, env []string, payload string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found on PATH")
	}
	cmd := exec.Command("bash", shim)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = strings.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running shim: %v", err)
	}
	return stdout.String(), code
}

func thisShim(t *testing.T) string {
	return filepath.Join(repoRootDir(t), "hooks", "pretooluse-gitsize.sh")
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	writeFile(t, path, []byte("#!/bin/sh\n"+body+"\n"))
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func bashPayload(command string) string {
	return `{"tool_name":"Bash","tool_input":{"command":"` + command + `"}}`
}

// copySource copies the plugin (source and hook) so a test can change it.
func copySource(t *testing.T) string {
	t.Helper()
	src, dst := repoRootDir(t), filepath.Join(t.TempDir(), "plugin")
	for _, part := range []string{"go.mod", "cmd", "internal", "hooks"} {
		err := filepath.WalkDir(filepath.Join(src, part), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			info, _ := d.Info()
			if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, rel), data, info.Mode().Perm())
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func goOnSystemPath() bool {
	for _, p := range []string{"/usr/bin/go", "/bin/go"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func TestShimNeverRunsRepositoryBinaries(t *testing.T) {
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	evil := `echo "$0" >> ` + marker + `; exit 1`
	for _, name := range []string{"gitsize", "cat", "dirname", "uname", "grep", "sed", "cksum", "find", "head", "git"} {
		writeScript(t, filepath.Join(repo, name), evil)
		writeScript(t, filepath.Join(repo, "node_modules", ".bin", name), evil)
	}
	writeScript(t, filepath.Join(repo, "bin", "gitsize"), evil)
	writeScript(t, filepath.Join(repo, "c:", "gitsize"), evil) // a drive-letter path is relative outside Windows

	env := []string{
		// Relative entries first: they would win over the system tools if trusted.
		"PATH=.:./node_modules/.bin:/usr/bin:/bin",
		"HOME=" + t.TempDir(),
		"CLAUDE_PLUGIN_DATA=" + t.TempDir(),
		"GITSIZE_BIN=c:/gitsize", // relative here, so it would resolve inside the repository
	}
	out, code := runShim(t, thisShim(t), repo, env, bashPayload("git add big.bin"))
	if code != 0 {
		t.Errorf("shim exited %d, want 0", code)
	}
	if ran, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the shim executed programs from the working repository:\n%s", ran)
	}
	if !goOnSystemPath() && !strings.Contains(out, `"ask"`) {
		t.Errorf("with no usable binary the shim should ask and say the guard is inactive; got %q", out)
	}
}

func TestShimUsesGitsizeBinAndFailsOpen(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-gitsize")
	writeScript(t, fake, `cat >/dev/null; echo '{"hookSpecificOutput":{"permissionDecision":"deny"}}'`)
	env := []string{"PATH=/usr/bin:/bin", "GITSIZE_BIN=" + fake} // HOME deliberately unset

	out, code := runShim(t, thisShim(t), dir, env, bashPayload("git add x"))
	if code != 0 || !strings.Contains(out, `"deny"`) {
		t.Errorf("GITSIZE_BIN: exit %d, output %q; want 0 with the binary's decision", code, out)
	}

	crash := filepath.Join(dir, "crash")
	writeScript(t, crash, `cat >/dev/null; echo 'panic: boom' >&2; echo partial; exit 2`)
	env[1] = "GITSIZE_BIN=" + crash
	out, code = runShim(t, thisShim(t), dir, env, bashPayload("git add x"))
	if code != 0 || out != "" {
		t.Errorf("crashing binary: exit %d, output %q; want 0 and no decision (fail open)", code, out)
	}
}

func TestShimFallbackLooksOnlyAtTheCommand(t *testing.T) {
	plugin := t.TempDir() // a hook with no source to build and no binary
	shim := filepath.Join(plugin, "hooks", "pretooluse-gitsize.sh")
	data, err := os.ReadFile(thisShim(t))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, shim, data)
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir()}

	tests := []struct {
		payload string
		ask     bool
	}{
		{`{"tool_name":"Bash","tool_input":{"command":"ls -la"},"cwd":"/src/git/commitizen"}`, false},
		{`{"tool_name":"Bash","tool_input":{"command":"git -C \"my dir\" add big.bin"}}`, true},
		{`{"tool_name":"Bash","tool_input":{"command":"cd sub\ngit add big.bin"}}`, true},
		{`{"tool_name":"Write","tool_input":{"file_path":"git-add.txt","content":"x"}}`, false},
	}
	for _, tc := range tests {
		out, _ := runShim(t, shim, plugin, env, tc.payload)
		if got := strings.Contains(out, `"ask"`); got != tc.ask {
			t.Errorf("payload %s: ask=%v (output %q), want %v", tc.payload, got, out, tc.ask)
		}
	}
}

func TestShimBuildsFromSource(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary; skipped in -short mode")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not found on PATH")
	}
	plugin := copySource(t)
	shim := filepath.Join(plugin, "hooks", "pretooluse-gitsize.sh")
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "big.bin"), randomBytes(t, 3*mib))
	data := t.TempDir()

	env := append(os.Environ(),
		"PATH="+filepath.Dir(goBin)+":/usr/bin:/bin",
		"CLAUDE_PLUGIN_DATA="+data,
		"GITSIZE_FAILURE_MB=2",
		"GOOS=plan9", "GOARCH=386", "GOFLAGS=-mod=vendor", // must not leak into the build
	)
	deny := func(env []string) {
		t.Helper()
		out, code := runShim(t, shim, repo, env, bashPayload("git add big.bin"))
		if code != 0 || !strings.Contains(out, `"deny"`) || !strings.Contains(out, "big.bin") {
			t.Fatalf("exit %d, output %q; want a deny naming big.bin", code, out)
		}
	}
	built := func() time.Time {
		t.Helper()
		bins, _ := filepath.Glob(filepath.Join(data, "bin", "*", "gitsize"))
		if len(bins) != 1 {
			t.Fatalf("expected one cached binary, found %v", bins)
		}
		info, err := os.Stat(bins[0])
		if err != nil {
			t.Fatal(err)
		}
		return info.ModTime()
	}

	deny(env)
	first := built()

	deny(env) // unchanged source: reuse
	if !built().Equal(first) {
		t.Error("the cached binary was rebuilt although the source did not change")
	}

	// Without go on PATH, an up-to-date cached binary is still used.
	stubDir := t.TempDir()
	writeScript(t, filepath.Join(stubDir, "go"), "exit 1")
	deny(append(env, "PATH="+stubDir+":/usr/bin:/bin"))

	// Touching the source without changing it keeps the build...
	future := time.Now().Add(time.Hour)
	src := filepath.Join(plugin, "internal", "analyzer", "types.go")
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatal(err)
	}
	deny(env)
	if !built().Equal(first) {
		t.Error("the cached binary was rebuilt although only an mtime changed")
	}

	// ...while changing its content triggers a rebuild.
	f, err := os.OpenFile(src, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("\n// changed\n")
	f.Close()
	deny(env)
	if built().Equal(first) {
		t.Error("the cached binary was not rebuilt after the source changed")
	}
}
