package analyzer

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAnalyzeFileSmall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping integration test")
	}

	repoDir := t.TempDir()
	if _, err := RunGitCommand(repoDir, "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Create a 200 KB text file — well under the default 50 MB warning threshold.
	testFile := filepath.Join(repoDir, "small.txt")
	data := make([]byte, 200*1024) // 200 KB of zero bytes
	for i := range data {
		data[i] = 'A' // fill with printable text so IsLikelyBinary returns false
	}
	if err := os.WriteFile(testFile, data, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	report, err := AnalyzeFile(repoDir, testFile, DefaultThresholds())
	if err != nil {
		t.Fatalf("AnalyzeFile returned error: %v", err)
	}

	if report.RiskLevel != RiskLow {
		t.Errorf("expected RiskLow for 200KB file, got %q", report.RiskLevel)
	}
	if report.GrowthBytes <= 0 {
		t.Errorf("expected positive GrowthBytes, got %d", report.GrowthBytes)
	}
	if len(report.Files) != 1 {
		t.Fatalf("expected 1 file in report, got %d", len(report.Files))
	}
	if report.Files[0].IsBinary {
		t.Error("expected text file to not be marked binary")
	}
	if report.Files[0].ChangeType != "added" {
		t.Errorf("expected ChangeType=added, got %q", report.Files[0].ChangeType)
	}
	if report.LargestFile == nil {
		t.Error("expected LargestFile to be non-nil")
	}
	if report.Recommendation != "" {
		t.Errorf("expected empty recommendation for low risk, got %q", report.Recommendation)
	}
	t.Logf("200 KB file: risk=%s, growth=%s", report.RiskLevel, HumanReadableSize(report.GrowthBytes))
}

func TestAnalyzeFileLarge(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping integration test")
	}

	repoDir := t.TempDir()
	if _, err := RunGitCommand(repoDir, "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Create a 60 MB file using os.Truncate — this is fast because the OS
	// allocates a sparse file (or zero-fills efficiently) rather than us
	// writing 60 million bytes in a loop. The file will contain null bytes,
	// so IsLikelyBinary will report it as binary.
	testFile := filepath.Join(repoDir, "large.bin")
	f, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	f.Close()

	targetSize := int64(60 * 1024 * 1024) // 60 MB
	if err := os.Truncate(testFile, targetSize); err != nil {
		t.Fatalf("failed to truncate file to 60MB: %v", err)
	}

	report, err := AnalyzeFile(repoDir, testFile, DefaultThresholds())
	if err != nil {
		t.Fatalf("AnalyzeFile returned error: %v", err)
	}

	// 60 MB is above the default 50 MB warning threshold but below
	// the 100 MB failure threshold, so we expect medium risk.
	// However, git's compressed object size may differ from the raw size,
	// so we accept medium OR high.
	if report.RiskLevel != RiskMedium && report.RiskLevel != RiskHigh {
		t.Errorf("expected RiskMedium or RiskHigh for 60MB file, got %q", report.RiskLevel)
	}
	if report.GrowthBytes <= 0 {
		t.Errorf("expected positive GrowthBytes, got %d", report.GrowthBytes)
	}
	if report.Recommendation == "" {
		t.Error("expected non-empty recommendation for medium/high risk")
	}
	if len(report.Reasons) == 0 {
		t.Error("expected non-empty reasons for medium/high risk")
	}
	t.Logf("60 MB file: risk=%s, growth=%s, recommendation=%q",
		report.RiskLevel, HumanReadableSize(report.GrowthBytes), report.Recommendation)
}

func TestAnalyzeFileDedupe(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH, skipping integration test")
	}

	repoDir := t.TempDir()
	if _, err := RunGitCommand(repoDir, "init"); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	// Write a file and add it to the object store manually.
	testFile := filepath.Join(repoDir, "dupe.txt")
	if err := os.WriteFile(testFile, []byte("duplicate content"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Pre-write the object so it already exists.
	if _, err := RunGitCommand(repoDir, "hash-object", "-w", testFile); err != nil {
		t.Fatalf("hash-object -w failed: %v", err)
	}

	report, err := AnalyzeFile(repoDir, testFile, DefaultThresholds())
	if err != nil {
		t.Fatalf("AnalyzeFile returned error: %v", err)
	}

	// Since the object already exists, growth should be 0 (dedupe case).
	if report.GrowthBytes != 0 {
		t.Errorf("expected GrowthBytes=0 for deduplicated file, got %d", report.GrowthBytes)
	}
	if report.RiskLevel != RiskLow {
		t.Errorf("expected RiskLow for deduplicated file, got %q", report.RiskLevel)
	}
	t.Logf("dedupe test: risk=%s, growth=%d", report.RiskLevel, report.GrowthBytes)
}
