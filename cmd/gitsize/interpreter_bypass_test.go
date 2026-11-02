package main

import (
	"path/filepath"
	"testing"
)

func TestInterpreterBypass(t *testing.T) {
	clearThresholdEnv(t)
	repo := newRepo(t)
	checkGit := assertGeneratedGitUnchanged(t, repo)
	defer checkGit()

	writeFile(t, filepath.Join(repo, "model.bin"), randomBytes(t, 3*mib))

	cmd := `python3 -c "import subprocess; subprocess.run(['git','add','model.bin'])" && git commit -m 'add model'`
	got, reason := hook(t, bash(repo, cmd))

	t.Logf("hook decision: %q, reason: %q", got, reason)
	if got == "" {
		t.Fatalf("interpreter bypass reproduced: hook returned no decision (empty string) for 3 MB model.bin with 2 MB failure threshold; want deny or ask")
	}
	if got != "deny" && got != "ask" {
		t.Fatalf("unexpected decision %q; want deny or ask", got)
	}
}
