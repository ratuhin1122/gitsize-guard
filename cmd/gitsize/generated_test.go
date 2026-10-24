package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
	"github.com/ratuhin1122/gitsize-guard/internal/hookio"
)

func TestGeneratedWritePayload(t *testing.T) {
	for _, tc := range []struct {
		name, path, content string
		th                  analyzer.Thresholds
		decision            hookio.Decision
		disabled            bool
	}{
		{"strong path low", filepath.Join("node_modules", "a.js"), "ordinary", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, hookio.Ask, false},
		{"strong path medium", filepath.Join("node_modules", "a.js"), "ordinary", analyzer.Thresholds{WarningBytes: 5, FailureBytes: 200}, hookio.Ask, false},
		{"strong path high", filepath.Join("node_modules", "a.js"), "ordinary", analyzer.Thresholds{WarningBytes: 5, FailureBytes: 8}, hookio.Deny, false},
		{"content marker", "output.go", "// @generated\npackage output", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, hookio.Ask, false},
		{"weak path", filepath.Join("dist", "a.js"), "ordinary", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, "", false},
		{"normal payload", "output.go", "package output", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, "", false},
		{"disabled", filepath.Join("node_modules", "a.js"), "ordinary", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			// Never classify the destination's existing bytes for a Write call.
			writeFile(t, filepath.Join(repo, tc.path), []byte("// @generated\nwrong on-disk content"))
			cfg := analyzer.DefaultGeneratedConfig()
			cfg.Enabled = !tc.disabled
			in := hookio.Input{ToolName: "Write", Cwd: repo, ToolInput: hookio.ToolInput{FilePath: tc.path, Content: tc.content}}
			decision, reason, growth := evaluateWithConfig(in, tc.th, analyzer.NearDupConfig{}, cfg)
			if decision != tc.decision {
				t.Fatalf("decision=%s reason=%s", decision, reason)
			}
			if decision != "" && (growth != int64(len(tc.content)) || !strings.Contains(reason, "looks like generated") || !strings.Contains(reason, "git rm -r --cached")) {
				t.Fatalf("growth=%d reason=%s", growth, reason)
			}
			r, _, err := analyzeWriteWithConfig(in.ToolInput, repo, tc.th, analyzer.NearDupConfig{}, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "weak path" && (r == nil || r.RiskLevel != analyzer.RiskLow || len(r.Notes) != 1 || r.Files[0].Generated.Confidence != "weak") {
				t.Fatalf("weak report=%+v", r)
			}
			if tc.name == "normal payload" && (r == nil || r.GeneratedFiles != 0) {
				t.Fatalf("used on-disk marker: %+v", r)
			}
		})
	}
}

func TestGeneratedBashDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		th            analyzer.Thresholds
		decision      hookio.Decision
	}{
		{"exact advisory", "git add node_modules", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, hookio.Ask},
		{"exact denial", "git add node_modules", analyzer.Thresholds{WarningBytes: 1, FailureBytes: 2}, hookio.Deny},
		{"estimated advisory", "xargs git add", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, hookio.Ask},
		{"compound denial", "echo changed > node_modules/a.js && git add node_modules", analyzer.Thresholds{WarningBytes: 100, FailureBytes: 200}, hookio.Deny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			writeFile(t, filepath.Join(repo, "node_modules", "a.js"), []byte("ordinary"))
			decision, reason, _ := evaluateWithConfig(hookio.Input{ToolName: "Bash", Cwd: repo, ToolInput: hookio.ToolInput{Command: tc.command}}, tc.th, analyzer.NearDupConfig{}, analyzer.DefaultGeneratedConfig())
			if decision != tc.decision || !strings.Contains(reason, "looks like generated") {
				t.Fatalf("decision=%s reason=%s", decision, reason)
			}
		})
	}
}

func TestGeneratedWriteTrackedFolderSuppressesContent(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "dist", "existing.js"), []byte("intentional"))
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "intentional dist")
	report, _, err := analyzeWriteWithConfig(hookio.ToolInput{
		FilePath: filepath.Join("dist", "new", "output.go"), Content: "// @generated\npackage output",
	}, repo, analyzer.DefaultThresholds(), analyzer.NearDupConfig{}, analyzer.DefaultGeneratedConfig())
	if err != nil || report == nil || report.GeneratedFiles != 0 || report.RiskLevel != analyzer.RiskLow || len(report.Notes) != 0 {
		t.Fatalf("suppressed Write report=%+v err=%v", report, err)
	}
}
