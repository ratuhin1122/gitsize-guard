package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

func TestGeneratedHookInjectedLimits(t *testing.T) {
	clearGeneratedEnv(t)
	clearNearDupEnv(t)
	clearThresholdEnv(t)
	repo := newRepo(t)
	for i := 0; i < 142; i++ {
		writeFile(t, filepath.Join(repo, "node_modules", fmt.Sprintf("%03d.js", i)), []byte("dependency"))
	}
	git(t, repo, "add", ".")
	defer assertGeneratedGitUnchanged(t, repo)()
	encoded, err := json.Marshal(bash(repo, "git add ."))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		cap, count int
		capped     bool
	}{
		{"large tree generous deadline", 5000, 142, false},
		{"injected path cap", 1, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runHookWithBudgets([]string{"--near-dup=false"}, bytes.NewReader(encoded), &stdout, &stderr, 0, time.Minute, tc.cap)
			var out decision
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatalf("output=%s stderr=%s err=%v", stdout.String(), stderr.String(), err)
			}
			reason := out.HookSpecificOutput.PermissionDecisionReason
			if code != 0 || stderr.Len() != 0 || out.HookSpecificOutput.PermissionDecision != "ask" || strings.Count(reason, "looks like generated or dependency files") != 1 || !strings.Contains(reason, fmt.Sprintf("(%d files,", tc.count)) || strings.Contains(reason, "time budget reached") || strings.Contains(reason, "limited to 1 paths") != tc.capped {
				t.Fatalf("code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

// This records current fail-open behavior; it deliberately does not change the
// policy. An exhausted advisory deadline alone is not turned into a decision.
func TestGeneratedHookTinyBudgetFailsOpen(t *testing.T) {
	clearGeneratedEnv(t)
	clearNearDupEnv(t)
	clearThresholdEnv(t)
	repo := newRepo(t)
	file := filepath.Join("node_modules", "app.js")
	writeFile(t, filepath.Join(repo, file), []byte("dependency"))
	defer assertGeneratedGitUnchanged(t, repo)()
	encoded, err := json.Marshal(bash(repo, "git add ."))
	if err != nil {
		t.Fatal(err)
	}
	var controlOut, controlErr bytes.Buffer
	if code := runHookWithBudgets([]string{"--near-dup=false"}, bytes.NewReader(encoded), &controlOut, &controlErr, 0, time.Minute, 5000); code != 0 || !strings.Contains(controlOut.String(), `"permissionDecision":"ask"`) || controlErr.Len() != 0 {
		t.Fatalf("control code=%d output=%s stderr=%s", code, controlOut.String(), controlErr.String())
	}
	cfg := analyzer.DefaultGeneratedConfig()
	cfg.TimeBudget = time.Nanosecond
	report, err := analyzer.AnalyzeWithConfig([]analyzer.Selection{{Dir: repo, Kind: analyzer.KindFiles, Pathspecs: []string{file}}}, analyzer.DefaultThresholds(), analyzer.NearDupConfig{}, cfg)
	if err != nil || report == nil || report.RiskLevel != analyzer.RiskLow || report.GeneratedFiles != 0 || len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "time budget reached") {
		t.Fatalf("tiny-budget report=%+v err=%v", report, err)
	}
	var stdout, stderr bytes.Buffer
	code := runHookWithBudgets([]string{"--near-dup=false"}, bytes.NewReader(encoded), &stdout, &stderr, 0, time.Nanosecond, 5000)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("current fail-open behavior changed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	t.Logf("exhausted generated budget: exit=%d stdout=%q stderr=%q; CLI skip note=%q", code, stdout.String(), stderr.String(), report.Notes[0])
}

// Advisory expiration must never erase a completed exact size denial.
func TestHookOversizedStagedFileDeniesWithExpiredAdvisoryBudgets(t *testing.T) {
	clearGeneratedEnv(t)
	clearNearDupEnv(t)
	clearThresholdEnv(t)
	repo := newRepo(t)
	file := filepath.Join("node_modules", "large.bin")
	content := bytes.Repeat([]byte{0, 1, 2, 3}, 3*mib/4)
	writeFile(t, filepath.Join(repo, file), content)
	git(t, repo, "add", "-f", "--", file)
	defer assertGeneratedGitUnchanged(t, repo)()
	encoded, err := json.Marshal(bash(repo, "git commit -m oversized-budget-regression"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := runHookWithBudgets([]string{
		"--warning-mb=1", "--failure-mb=2", "--near-dup-min-mb=1",
	}, bytes.NewReader(encoded), &stdout, &stderr, time.Nanosecond, time.Nanosecond, 5000)
	t.Logf("hook exit=%d", code)
	t.Logf("hook stdout:\n%s", stdout.String())
	t.Logf("hook stderr=%q", stderr.String())
	if stdout.Len() == 0 {
		t.Fatalf("HOOK FAILED OPEN: exit=%d stderr=%q; staged 3 MiB file must be denied at the 2 MiB failure threshold", code, stderr.String())
	}
	var out decision
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("invalid hook JSON: %v; stdout=%q", err, stdout.String())
	}
	reason := out.HookSpecificOutput.PermissionDecisionReason
	if code != 0 || stderr.Len() != 0 || out.HookSpecificOutput.HookEventName != "PreToolUse" || out.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("expected deny: exit=%d stdout=%s stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, required := range []string{
		"gitsize-guard blocked this:", filepath.ToSlash(file),
		analyzer.HumanReadableSize(int64(len(content))),
		"failure threshold " + analyzer.HumanReadableSize(2*mib),
		"Near-duplicate discovery skipped: time budget reached.",
		"Generated-file check skipped: time budget reached.",
	} {
		if !strings.Contains(reason, required) {
			t.Errorf("missing %q from denial reason: %s", required, reason)
		}
	}
}
