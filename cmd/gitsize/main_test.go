package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

func TestCLIRun(t *testing.T) {
	t.Run("no arguments exits 2 with usage", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("expected exit code 2, got %d", code)
		}
		if !strings.Contains(stderr.String(), "Usage: gitsize") {
			t.Errorf("expected usage on stderr, got %q", stderr.String())
		}
	})

	t.Run("unknown subcommand exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"foobar"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("expected exit code 2, got %d", code)
		}
		if !strings.Contains(stderr.String(), "unknown subcommand") {
			t.Errorf("expected unknown subcommand error, got %q", stderr.String())
		}
	})

	t.Run("missing required flags exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"analyze", "--repo", "."}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("expected exit code 2, got %d", code)
		}
		if !strings.Contains(stderr.String(), "both --repo and --file are required") {
			t.Errorf("expected required flag error, got %q", stderr.String())
		}
	})

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping git-dependent CLI tests")
	}

	repoDir := t.TempDir()
	if _, err := analyzer.RunGitCommand(repoDir, "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	testFile := filepath.Join(repoDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	t.Run("valid analyze on small file exits 0 with JSON output", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"analyze", "--repo", repoDir, "--file", "test.txt"}, &stdout, &stderr)
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
	})

	t.Run("high risk analysis exits 1", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		// Setting failure-mb to 0 forces any file >0 bytes to be high risk
		code := run([]string{"analyze", "--repo", repoDir, "--file", "test.txt", "--failure-mb", "0"}, &stdout, &stderr)
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

	t.Run("analysis error on nonexistent file exits 2", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"analyze", "--repo", repoDir, "--file", "nonexistent.txt"}, &stdout, &stderr)
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
}
