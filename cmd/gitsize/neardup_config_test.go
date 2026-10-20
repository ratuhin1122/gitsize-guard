package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

func clearNearDupEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GITSIZE_NEARDUP", "GITSIZE_NEARDUP_THRESHOLD", "GITSIZE_NEARDUP_MIN_MB"} {
		t.Setenv(name, "")
	}
}

func TestHookHelpDoesNotReadPayload(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"hook", "--help"}, nil, &stdout, &stderr); code != 0 || stdout.Len() != 0 {
		t.Fatalf("help: code=%d stdout=%s", code, stdout.String())
	}
}

func TestResolveNearDup(t *testing.T) {
	defaults := analyzer.DefaultNearDupConfig()
	for _, tc := range []struct {
		name, enabled, threshold, min string
		args                          []string
		want                          analyzer.NearDupConfig
		wantErr                       string
	}{
		{"defaults", "", "", "", nil, defaults, ""},
		{"environment", "0", "0.95", "12", nil, analyzer.NearDupConfig{Enabled: false, Threshold: .95, MinBytes: 12 * mib}, ""},
		{"trimmed environment", " 1 ", " 0.5 ", " 1 ", nil, analyzer.NearDupConfig{Enabled: true, Threshold: .5, MinBytes: mib}, ""},
		{"flags override invalid environment", "bad", "bad", "bad", []string{"--near-dup=false", "--near-dup-threshold=1", "--near-dup-min-mb=2"}, analyzer.NearDupConfig{Enabled: false, Threshold: 1, MinBytes: 2 * mib}, ""},
		{"true flag overrides disabled environment", "0", "", "", []string{"--near-dup"}, defaults, ""},
		{"boolean must be zero or one", "true", "", "", nil, defaults, "GITSIZE_NEARDUP"},
		{"invalid threshold keeps valid fields", "0", "bad", "12", nil, analyzer.NearDupConfig{Enabled: false, Threshold: .9, MinBytes: 12 * mib}, "GITSIZE_NEARDUP_THRESHOLD"},
		{"threshold below range", "", ".49", "", nil, defaults, "0.5 to 1.0"},
		{"threshold above range", "", "1.01", "", nil, defaults, "0.5 to 1.0"},
		{"nan", "", "NaN", "", nil, defaults, "0.5 to 1.0"},
		{"infinity", "", "+Inf", "", nil, defaults, "0.5 to 1.0"},
		{"minimum not integer", "", "", "1.5", nil, defaults, "whole number"},
		{"minimum zero", "", "", "0", nil, defaults, "near-dup-min-mb"},
		{"minimum negative", "", "", "-1", nil, defaults, "near-dup-min-mb"},
		{"minimum too large", "", "", "1048577", nil, defaults, "near-dup-min-mb"},
		{"bad flag range", "", "", "", []string{"--near-dup-threshold=.1", "--near-dup-min-mb=0"}, defaults, "near-dup-threshold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITSIZE_NEARDUP", tc.enabled)
			t.Setenv("GITSIZE_NEARDUP_THRESHOLD", tc.threshold)
			t.Setenv("GITSIZE_NEARDUP_MIN_MB", tc.min)
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			opts := nearDupFlags(fs)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			got, err := resolveNearDup(fs, opts)
			if tc.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("error=%v, want %q", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("config=%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestNearDupInvalidConfigurationSurfaces(t *testing.T) {
	clearThresholdEnv(t)
	clearNearDupEnv(t)
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "small.txt"), []byte("hello"))
	for _, tc := range []struct {
		name, env, value string
		args             []string
	}{
		{"bad boolean env", "GITSIZE_NEARDUP", "yes", nil},
		{"bad float env", "GITSIZE_NEARDUP_THRESHOLD", "oops", nil},
		{"nan env", "GITSIZE_NEARDUP_THRESHOLD", "NaN", nil},
		{"range env", "GITSIZE_NEARDUP_THRESHOLD", "0.4", nil},
		{"bad minimum env", "GITSIZE_NEARDUP_MIN_MB", "zero", nil},
		{"zero minimum env", "GITSIZE_NEARDUP_MIN_MB", "0", nil},
		{"boolean flag", "", "", []string{"--near-dup=oops"}},
		{"float flag", "", "", []string{"--near-dup-threshold=oops"}},
		{"float flag range", "", "", []string{"--near-dup-threshold=2"}},
		{"minimum flag", "", "", []string{"--near-dup-min-mb=oops"}},
		{"minimum flag range", "", "", []string{"--near-dup-min-mb=-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
			}
			for _, payload := range []map[string]any{bash(repo, "git add small.txt"), write(repo, "small.txt", "hello")} {
				encoded, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				code := run(append([]string{"hook"}, tc.args...), bytes.NewReader(encoded), &stdout, &stderr)
				var out decision
				if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
					t.Fatalf("output=%q stderr=%s: %v", stdout.String(), stderr.String(), err)
				}
				if code != 0 || out.HookSpecificOutput.PermissionDecision != "ask" || !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "configuration error") {
					t.Fatalf("code=%d output=%s", code, stdout.String())
				}
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"analyze", "--repo", repo, "--pending"}, tc.args...)
			if code := run(args, nil, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("invalid analyze config: code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestHookModifiedTwelveMBNearDuplicate(t *testing.T) {
	clearThresholdEnv(t)
	clearNearDupEnv(t)
	repo := newRepo(t)
	data := randomBytes(t, 12*mib)
	writeFile(t, filepath.Join(repo, "original.bin"), data)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-qm", "original")
	copyData := bytes.Clone(data)
	for i := len(copyData) / 2; i < len(copyData)/2+100; i++ {
		copyData[i] ^= 0xff
	}
	writeFile(t, filepath.Join(repo, "modified-copy.bin"), copyData)
	payload, err := json.Marshal(bash(repo, "git add modified-copy.bin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		env  string
		want string
	}{
		{"defaults ask", nil, "", "ask"},
		{"env disables", nil, "0", ""},
		{"flag reenables", []string{"--near-dup=true"}, "0", "ask"},
		{"flag disables", []string{"--near-dup=false"}, "1", ""},
		{"minimum excludes", []string{"--near-dup-min-mb=13"}, "", ""},
		{"threshold excludes", []string{"--near-dup-threshold=1"}, "", ""},
		{"high risk denies with advisory", []string{"--warning-mb=1", "--failure-mb=2"}, "", "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GITSIZE_NEARDUP", tc.env)
			var stdout, stderr bytes.Buffer
			if code := run(append([]string{"hook"}, tc.args...), bytes.NewReader(payload), &stdout, &stderr); code != 0 {
				t.Fatal(code)
			}
			if tc.want == "" {
				if stdout.Len() != 0 {
					t.Fatalf("expected silence, got %s", stdout.String())
				}
				return
			}
			var out decision
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.HookSpecificOutput.PermissionDecision != tc.want || !strings.Contains(out.HookSpecificOutput.PermissionDecisionReason, "identical to original.bin already in HEAD") {
				t.Fatalf("output=%s stderr=%s", stdout.String(), stderr.String())
			}
		})
	}
	// Exercise CLI wiring as well as the shared flag resolver.
	var stdout, stderr bytes.Buffer
	if code := run([]string{"analyze", "--repo", repo, "--file", filepath.Join(repo, "modified-copy.bin"), "--near-dup=false"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("analyze: %d %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), `"near_duplicate`) {
		t.Fatalf("disabled CLI detection: %s", stdout.String())
	}
}
