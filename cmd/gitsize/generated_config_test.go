package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

func clearGeneratedEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GITSIZE_GENERATED", "GITSIZE_GENERATED_ALLOW", "GITSIZE_GENERATED_STRICT_WEAK"} {
		t.Setenv(name, "")
	}
}

func TestResolveGenerated(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, strict, allow string
		args                         []string
		want                         analyzer.GeneratedConfig
		wantErr                      bool
	}{
		{"defaults", "", "", "", nil, analyzer.GeneratedConfig{Enabled: true}, false},
		{"environment", "0", "1", "dist/**, node_modules/**", nil, analyzer.GeneratedConfig{StrictWeak: true, Allow: []string{"dist/**", "node_modules/**"}}, false},
		{"trimmed booleans", " 1 ", " 0 ", "", nil, analyzer.GeneratedConfig{Enabled: true}, false},
		{"flags override invalid env", "yes", "yes", "[", []string{"--generated=false", "--generated-strict-weak", "--generated-allow=dist/**", "--generated-allow=vendor/**"}, analyzer.GeneratedConfig{StrictWeak: true, Allow: []string{"dist/**", "vendor/**"}}, false},
		{"enabled env invalid", "true", "", "", nil, analyzer.GeneratedConfig{Enabled: true}, true},
		{"strict env invalid", "0", "false", "", nil, analyzer.GeneratedConfig{}, true},
		{"invalid env glob", "", "", "[", nil, analyzer.GeneratedConfig{Enabled: true, Allow: []string{}}, true},
		{"keep valid globs", "", "", "dist/**,[", nil, analyzer.GeneratedConfig{Enabled: true, Allow: []string{"dist/**"}}, true},
		{"empty comma entry invalid", "", "", "dist/**,", nil, analyzer.GeneratedConfig{Enabled: true, Allow: []string{"dist/**"}}, true},
		{"invalid flag glob", "", "", "", []string{"--generated-allow=["}, analyzer.GeneratedConfig{Enabled: true, Allow: []string{}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITSIZE_GENERATED", tc.enabled)
			t.Setenv("GITSIZE_GENERATED_STRICT_WEAK", tc.strict)
			t.Setenv("GITSIZE_GENERATED_ALLOW", tc.allow)
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			opts := generatedFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			cfg, err := resolveGenerated(fs, opts)
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(cfg, tc.want) {
				t.Fatalf("cfg=%+v err=%v want=%+v", cfg, err, tc.want)
			}
		})
	}
}

func TestGeneratedAllowPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, file, env string
		args            []string
		count           int
	}{
		{"file fallback", "node_modules/**\n", "", nil, 0},
		{"environment replaces file", "node_modules/**\n", "dist/**", nil, 1},
		{"flag replaces environment", "dist/**\n", "dist/**", []string{"--generated-allow=node_modules/**"}, 0},
		{"flag does not merge environment", "node_modules/**\n", "node_modules/**", []string{"--generated-allow=dist/**"}, 1},
		{"environment overrides invalid file", "[\n", "node_modules/**", nil, 0},
		{"flag overrides invalid sources", "[\n", "[", []string{"--generated-allow=node_modules/**"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearGeneratedEnv(t)
			clearNearDupEnv(t)
			clearThresholdEnv(t)
			repo := newRepo(t)
			writeFile(t, filepath.Join(repo, ".gitsizeallow"), []byte(tc.file))
			writeFile(t, filepath.Join(repo, "node_modules", "a.js"), []byte("dependency"))
			t.Setenv("GITSIZE_GENERATED_ALLOW", tc.env)
			var stdout, stderr bytes.Buffer
			args := append([]string{"analyze", "--repo", repo, "--pending", "--near-dup=false"}, tc.args...)
			if code := run(args, nil, &stdout, &stderr); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			var report analyzer.Report
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.GeneratedFiles != tc.count || len(report.Notes) != 0 {
				t.Fatalf("report=%s", stdout.String())
			}
		})
	}
}

func TestGeneratedInvalidConfigurationHook(t *testing.T) {
	for _, tc := range []struct {
		name, env, value, file string
		args                   []string
	}{
		{"boolean env", "GITSIZE_GENERATED", "yes", "", nil},
		{"strict env", "GITSIZE_GENERATED_STRICT_WEAK", "true", "", nil},
		{"glob env", "GITSIZE_GENERATED_ALLOW", "[", "", nil},
		{"boolean flag", "", "", "", []string{"--generated=oops"}},
		{"strict flag", "", "", "", []string{"--generated-strict-weak=oops"}},
		{"glob flag", "", "", "", []string{"--generated-allow=["}},
		{"file glob", "", "", "[\n", nil},
		{"file while disabled", "", "", "[\n", []string{"--generated=false", "--near-dup=false"}},
		{"oversized file", "", "", strings.Repeat("#", (64<<10)+1), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearGeneratedEnv(t)
			clearNearDupEnv(t)
			clearThresholdEnv(t)
			repo := newRepo(t)
			writeFile(t, filepath.Join(repo, "small.txt"), []byte("ordinary"))
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			if tc.file != "" {
				writeFile(t, filepath.Join(repo, ".gitsizeallow"), []byte(tc.file))
			}
			for _, payload := range []map[string]any{bash(repo, "git add small.txt"), write(repo, "small.txt", "ordinary")} {
				encoded, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				code := run(append([]string{"hook"}, tc.args...), bytes.NewReader(encoded), &stdout, &stderr)
				var out decision
				if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
					t.Fatalf("output=%s stderr=%s err=%v", stdout.String(), stderr.String(), err)
				}
				if code != 0 || out.HookSpecificOutput.PermissionDecision != "ask" || !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "configuration error") {
					t.Fatalf("code=%d output=%s", code, stdout.String())
				}
			}
			var stdout, stderr bytes.Buffer
			if code := run(append([]string{"analyze", "--repo", repo, "--pending"}, tc.args...), nil, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("CLI code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestGeneratedStagedHookPayload(t *testing.T) {
	clearGeneratedEnv(t)
	clearNearDupEnv(t)
	clearThresholdEnv(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "node_modules", "pkg", "index.js"), []byte("dependency"))
	git(t, repo, "add", ".")
	decision, reason := hook(t, bash(repo, "git add ."))
	if decision != "ask" || !strings.Contains(reason, "node_modules/ looks like generated or dependency files") {
		t.Fatalf("decision=%s reason=%s", decision, reason)
	}
	// The explicit disable flag suppresses only this advisory.
	encoded, _ := json.Marshal(bash(repo, "git add ."))
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "--generated=false"}, bytes.NewReader(encoded), &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Fatalf("disabled code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestGeneratedInvalidFileKeepsDenial(t *testing.T) {
	clearGeneratedEnv(t)
	clearNearDupEnv(t)
	clearThresholdEnv(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, ".gitsizeallow"), []byte("[\n"))
	writeFile(t, filepath.Join(repo, "node_modules", "a.js"), []byte("dependency"))
	for _, payload := range []map[string]any{bash(repo, "git add node_modules"), write(repo, filepath.Join("node_modules", "a.js"), "dependency")} {
		encoded, _ := json.Marshal(payload)
		var stdout, stderr bytes.Buffer
		code := run([]string{"hook", "--warning-mb=0", "--failure-mb=0"}, bytes.NewReader(encoded), &stdout, &stderr)
		var out decision
		if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
			t.Fatalf("output=%s err=%v", stdout.String(), err)
		}
		if code != 0 || out.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "configuration error") || !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "looks like generated") {
			t.Fatalf("output=%s", stdout.String())
		}
	}
}

func TestGeneratedStrictWeakHookAndWriteAllow(t *testing.T) {
	clearGeneratedEnv(t)
	clearNearDupEnv(t)
	clearThresholdEnv(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "dist", "app.js"), []byte("ordinary"))
	t.Setenv("GITSIZE_GENERATED_STRICT_WEAK", "1")
	got, reason := hook(t, bash(repo, "git add dist"))
	if got != "ask" || !strings.Contains(reason, "dist/ looks like generated") {
		t.Fatalf("decision=%s reason=%s", got, reason)
	}
	for _, tc := range []struct {
		payload map[string]any
		args    []string
	}{
		{bash(repo, "git add dist"), []string{"--generated-strict-weak=false"}},
		{bash(repo, "git add dist"), []string{"--generated-allow=dist/**"}},
		{write(repo, filepath.Join("node_modules", "a.js"), "// @generated"), []string{"--generated-allow=node_modules/**"}},
	} {
		encoded, _ := json.Marshal(tc.payload)
		var stdout, stderr bytes.Buffer
		if code := run(append([]string{"hook"}, tc.args...), bytes.NewReader(encoded), &stdout, &stderr); code != 0 || stdout.Len() != 0 {
			t.Fatalf("code=%d output=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	}
}
