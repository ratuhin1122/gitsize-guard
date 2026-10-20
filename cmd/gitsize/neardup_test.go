package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
	"github.com/ratuhin1122/gitsize-guard/internal/hookio"
)

func TestWriteNearDuplicateUsesPayload(t *testing.T) {
	repo := newRepo(t)
	content := strings.Repeat("0123456789abcdef", 6*mib/16)
	writeFile(t, filepath.Join(repo, "existing.bin"), []byte(content))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "existing")
	// The destination's on-disk contents must not be used by Write analysis.
	writeFile(t, filepath.Join(repo, "copy.bin"), []byte("unrelated on-disk bytes"))
	ti := hookio.ToolInput{FilePath: "copy.bin", Content: content}
	for _, tc := range []struct {
		name       string
		thresholds analyzer.Thresholds
		risk       string
		decision   hookio.Decision
	}{
		{"low to medium asks", analyzer.Thresholds{WarningBytes: 10 * mib, FailureBytes: 20 * mib}, analyzer.RiskMedium, hookio.Ask},
		{"medium stays medium", analyzer.Thresholds{WarningBytes: mib, FailureBytes: 20 * mib}, analyzer.RiskMedium, hookio.Ask},
		{"high stays high", analyzer.Thresholds{WarningBytes: mib, FailureBytes: 2 * mib}, analyzer.RiskHigh, hookio.Deny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report, exact, err := analyzeWrite(ti, repo, tc.thresholds)
			if err != nil || !exact || report == nil {
				t.Fatalf("Write: %+v, exact=%v, %v", report, exact, err)
			}
			if report.GrowthBytes != int64(len(content)) || report.RiskLevel != tc.risk || report.NearDuplicates != 1 {
				t.Fatalf("report = %+v", report)
			}
			if dup := report.Files[0].NearDuplicate; dup == nil || dup.Path != "existing.bin" || dup.Similarity != 1 {
				t.Fatalf("advisory = %+v", dup)
			}
			decision, reason, growth := evaluate(hookio.Input{ToolName: "Write", Cwd: repo, ToolInput: ti}, tc.thresholds)
			if decision != tc.decision || growth != int64(len(content)) || !strings.Contains(reason, "identical to existing.bin already in HEAD") {
				t.Fatalf("decision=%s growth=%d reason=%s", decision, growth, reason)
			}
			if tc.name == "low to medium asks" && strings.Contains(reason, "warning threshold") {
				t.Fatal("advisory incorrectly claims a size threshold was crossed")
			}
		})
	}
	// Replacing the existing path is not adding a second copy.
	report, _, err := analyzeWrite(hookio.ToolInput{FilePath: "existing.bin", Content: content}, repo, analyzer.DefaultThresholds())
	if err != nil || report == nil || report.NearDuplicates != 0 || report.RiskLevel != analyzer.RiskLow {
		t.Fatalf("same path: %+v, %v", report, err)
	}
}

func TestBashNearDuplicateAsks(t *testing.T) {
	repo := newRepo(t)
	content := strings.Repeat("0123456789abcdef", 5*mib/16)
	writeFile(t, filepath.Join(repo, "existing.bin"), []byte(content))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "existing")
	writeFile(t, filepath.Join(repo, "copy.bin"), []byte(content))
	decision, reason, growth := evaluate(hookio.Input{ToolName: "Bash", Cwd: repo, ToolInput: hookio.ToolInput{Command: "git add copy.bin"}}, analyzer.DefaultThresholds())
	if decision != hookio.Ask || growth != 0 || !strings.Contains(reason, "identical to existing.bin already in HEAD") {
		t.Fatalf("decision=%s growth=%d reason=%s", decision, growth, reason)
	}
}
