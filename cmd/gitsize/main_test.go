package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

const mib = 1024 * 1024

// newRepo creates a git repository with one committed file.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping git-dependent test")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "config", "user.email", "test@test.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")
	writeFile(t, filepath.Join(dir, "README.md"), []byte("# test\n"))
	git(t, dir, "add", "README.md")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := analyzer.RunGitCommand(dir, args...); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	b[0] = 0
	return b
}

// clearThresholdEnv stops the developer's own settings leaking into tests.
func clearThresholdEnv(t *testing.T) {
	t.Setenv("GITSIZE_WARNING_MB", "")
	t.Setenv("GITSIZE_FAILURE_MB", "")
}

func TestCLIRun(t *testing.T) {
	clearThresholdEnv(t)

	t.Run("no arguments exits 2 with usage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{}, nil, &stdout, &stderr)
		if code != 2 {
			t.Errorf("expected exit code 2, got %d", code)
		}
		if !strings.Contains(stderr.String(), "Usage: gitsize") {
			t.Errorf("expected usage on stderr, got %q", stderr.String())
		}
	})

	t.Run("unknown subcommand exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"foobar"}, nil, &stdout, &stderr)
		if code != 2 {
			t.Errorf("expected exit code 2, got %d", code)
		}
		if !strings.Contains(stderr.String(), "unknown subcommand") {
			t.Errorf("expected unknown subcommand error, got %q", stderr.String())
		}
	})

	t.Run("nothing to analyze exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"analyze", "--repo", "."}, nil, &stdout, &stderr)
		if code != 2 {
			t.Errorf("expected exit code 2, got %d", code)
		}
		if !strings.Contains(stderr.String(), "nothing to analyze") {
			t.Errorf("expected nothing-to-analyze error, got %q", stderr.String())
		}
	})

	t.Run("invalid thresholds exit 2", func(t *testing.T) {
		for _, args := range [][]string{
			{"--warning-mb", "-1"},
			{"--warning-mb", "200", "--failure-mb", "100"},
		} {
			var stdout, stderr bytes.Buffer
			code := run(append([]string{"analyze", "--file", "x"}, args...), nil, &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), "threshold") {
				t.Errorf("%v: exit %d, stderr %q; want 2 with a threshold error", args, code, stderr.String())
			}
		}
	})

	repoDir := newRepo(t)
	testFile := filepath.Join(repoDir, "test.txt")
	writeFile(t, testFile, []byte("test content"))

	t.Run("valid analyze on small file exits 0 with JSON output", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"analyze", "--repo", repoDir, "--file", testFile}, nil, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0, got %d; stderr: %s", code, stderr.String())
		}

		var report analyzer.Report
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("output is not valid JSON: %v; output was: %s", err, stdout.String())
		}
		if report.RiskLevel != analyzer.RiskLow {
			t.Errorf("expected risk_level=low, got %s", report.RiskLevel)
		}
		if report.LargestFile == nil || report.LargestFile.FilePath != "test.txt" || report.GrowthBytes != 12 {
			t.Errorf("unexpected report: %+v", report)
		}
	})

	t.Run("relative --file resolves against the current directory", func(t *testing.T) {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(repoDir); err != nil {
			t.Fatal(err)
		}
		defer os.Chdir(wd)
		var stdout, stderr bytes.Buffer
		if code := run([]string{"analyze", "--file", "test.txt"}, nil, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d; stderr: %s", code, stderr.String())
		}
	})

	t.Run("high risk analysis exits 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		// Zero thresholds make any growth high risk.
		code := run([]string{"analyze", "--repo", repoDir, "--file", testFile, "--warning-mb", "0", "--failure-mb", "0"}, nil, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr.String())
		}

		var report analyzer.Report
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("output is not valid JSON: %v; output was: %s", err, stdout.String())
		}
		if report.RiskLevel != analyzer.RiskHigh {
			t.Errorf("expected risk_level=high, got %s", report.RiskLevel)
		}
	})

	t.Run("thresholds from the environment", func(t *testing.T) {
		t.Setenv("GITSIZE_WARNING_MB", "0")
		t.Setenv("GITSIZE_FAILURE_MB", "0")
		var stdout, stderr bytes.Buffer
		if code := run([]string{"analyze", "--repo", repoDir, "--file", testFile}, nil, &stdout, &stderr); code != 1 {
			t.Fatalf("expected exit code 1, got %d; stderr: %s", code, stderr.String())
		}
	})

	t.Run("staged analysis", func(t *testing.T) {
		writeFile(t, filepath.Join(repoDir, "big.bin"), randomBytes(t, 3*mib))
		git(t, repoDir, "add", "big.bin")
		defer git(t, repoDir, "reset", "-q", "big.bin")
		var stdout, stderr bytes.Buffer
		code := run([]string{"analyze", "--repo", repoDir, "--staged", "--warning-mb", "1", "--failure-mb", "2"}, nil, &stdout, &stderr)
		if code != 1 || !strings.Contains(stdout.String(), `"big.bin"`) {
			t.Fatalf("exit %d, stdout %s, stderr %s; want 1 naming big.bin", code, stdout.String(), stderr.String())
		}
	})

	t.Run("analysis error on nonexistent file exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"analyze", "--repo", repoDir, "--file", filepath.Join(repoDir, "nonexistent.txt")}, nil, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("expected exit code 2, got %d", code)
		}
		if !strings.Contains(stderr.String(), "analysis error") {
			t.Errorf("expected analysis error message, got %q", stderr.String())
		}
		if stdout.Len() > 0 {
			t.Errorf("expected empty stdout on analysis error, got %q", stdout.String())
		}
	})

	t.Run("not a repository exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"analyze", "--repo", t.TempDir(), "--staged"}, nil, &stdout, &stderr); code != 2 {
			t.Fatalf("expected exit code 2, got %d", code)
		}
	})
}

func TestResolveThresholds(t *testing.T) {
	tests := []struct {
		name               string
		warnEnv, failEnv   string
		args               []string
		wantWarn, wantFail int64
		wantErr            string
	}{
		{"defaults", "", "", nil, 50, 100, ""},
		{"stricter failure alone lowers the warning", "", "40", nil, 40, 40, ""},
		{"higher warning alone raises the failure", "150", "", nil, 150, 150, ""},
		{"both set and inverted", "60", "40", nil, 0, 0, "exceeds"},
		{"flag overrides env", "", "40", []string{"--failure-mb", "200"}, 50, 200, ""},
		{"not a number", "", "40MB", nil, 0, 0, "whole number"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITSIZE_WARNING_MB", tc.warnEnv)
			t.Setenv("GITSIZE_FAILURE_MB", tc.failEnv)
			fs := flag.NewFlagSet("x", flag.ContinueOnError)
			w, f := thresholdFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			th, err := resolveThresholds(fs, *w, *f)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if th.WarningBytes != tc.wantWarn*mib || th.FailureBytes != tc.wantFail*mib {
				t.Errorf("thresholds = %d/%d MB, want %d/%d", th.WarningBytes/mib, th.FailureBytes/mib, tc.wantWarn, tc.wantFail)
			}
		})
	}
}

func TestHookSurfacesConfigErrors(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "small.txt"), []byte("hi"))
	t.Setenv("GITSIZE_FAILURE_MB", "40MB")
	in := `{"tool_name":"Bash","tool_input":{"command":"git add small.txt"},"cwd":"` + repo + `"}`
	var stdout, stderr bytes.Buffer
	run([]string{"hook"}, strings.NewReader(in), &stdout, &stderr)
	if !strings.Contains(stdout.String(), `"ask"`) || !strings.Contains(stdout.String(), "configuration error") {
		t.Errorf("invalid GITSIZE_FAILURE_MB should be surfaced on git staging commands; got %q", stdout.String())
	}
	stdout.Reset()
	run([]string{"hook"}, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"},"cwd":"`+repo+`"}`), &stdout, &stderr)
	if stdout.Len() != 0 {
		t.Errorf("unrelated commands should stay silent; got %q", stdout.String())
	}
}
