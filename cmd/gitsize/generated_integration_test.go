package main

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
	"github.com/ratuhin1122/gitsize-guard/internal/hookio"
)

// snapshotGeneratedGit includes the index, objects, refs, config, and every other
// file in .git. Comparing the full maps detects additions and deletions as well
// as changes; all fixture writes must finish before taking the first snapshot.
func snapshotGeneratedGit(t *testing.T, repo string) map[string][sha256.Size]byte {
	t.Helper()
	root := filepath.Join(repo, ".git")
	checksums := map[string][sha256.Size]byte{}
	err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		reader, err := os.Open(file)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, reader)
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		var digest [sha256.Size]byte
		copy(digest[:], hash.Sum(nil))
		checksums[rel] = digest
		return nil
	})
	if err != nil {
		t.Fatalf("SHA-256 snapshot of .git: %v", err)
	}
	return checksums
}

func assertGeneratedGitUnchanged(t *testing.T, repo string) func() {
	t.Helper()
	before := snapshotGeneratedGit(t, repo)
	return func() {
		t.Helper()
		after := snapshotGeneratedGit(t, repo)
		if !reflect.DeepEqual(before, after) {
			for file, digest := range before {
				if got, ok := after[file]; !ok || got != digest {
					t.Errorf("analysis changed or deleted .git file %q", file)
				}
			}
			for file := range after {
				if _, ok := before[file]; !ok {
					t.Errorf("analysis created .git file %q", file)
				}
			}
		} else {
			t.Logf("git_unchanged=true files=%d (SHA-256 of every .git file)", len(before))
		}
	}
}

func TestGeneratedReadOnlyIntegration(t *testing.T) {
	// newRepo uses t.TempDir and skips when Git is not on PATH. Each subtest
	// owns its repository; none changes the process cwd or runs in parallel.
	clearGeneratedEnv(t)
	clearNearDupEnv(t)
	clearThresholdEnv(t)
	generatedCfg := analyzer.DefaultGeneratedConfig()
	generatedCfg.TimeBudget = time.Minute
	thresholds := analyzer.DefaultThresholds()

	for _, tc := range []struct {
		name                 string
		setup                func(*testing.T, string) string
		count                int
		category, confidence string
		ignored, weak        bool
	}{
		{
			name: "many staged dependencies aggregate once",
			setup: func(t *testing.T, repo string) string {
				for i := 0; i < 142; i++ {
					writeFile(t, filepath.Join(repo, "node_modules", "pkg", fmt.Sprintf("file-%03d.js", i)), []byte("export const value = 1;\n"))
				}
				git(t, repo, "add", "--", "node_modules")
				return "git add ."
			}, count: 142, category: "dependencies", confidence: "strong",
		},
		{
			name: "new staged dist is weak note only",
			setup: func(t *testing.T, repo string) string {
				file := filepath.Join("dist", "bundle.js")
				writeFile(t, filepath.Join(repo, file), []byte("export const value = 1;\n"))
				git(t, repo, "add", "--", file)
				return "git commit -m integration"
			}, count: 1, category: "build-output", confidence: "weak", weak: true,
		},
		{
			name: "HEAD tracked dist is suppressed",
			setup: func(t *testing.T, repo string) string {
				old := filepath.Join("dist", "existing.js")
				writeFile(t, filepath.Join(repo, old), []byte("intentional output\n"))
				git(t, repo, "add", "--", old)
				git(t, repo, "commit", "-qm", "intentional dist")
				file := filepath.Join("dist", "bundle.js")
				writeFile(t, filepath.Join(repo, file), []byte("export const value = 1;\n"))
				git(t, repo, "add", "--", file)
				return "git commit -m integration"
			},
		},
		{
			name: "force added ignored path is strong",
			setup: func(t *testing.T, repo string) string {
				writeFile(t, filepath.Join(repo, ".gitignore"), []byte(".cache/\n"))
				file := filepath.Join(".cache", "artifact.txt")
				writeFile(t, filepath.Join(repo, file), []byte("cached output\n"))
				git(t, repo, "add", "--", ".gitignore")
				git(t, repo, "add", "-f", "--", file)
				return "git commit -m integration"
			}, count: 1, category: "cache", confidence: "strong", ignored: true,
		},
		{
			name: "allowlisted staged dependencies are silent",
			setup: func(t *testing.T, repo string) string {
				writeFile(t, filepath.Join(repo, ".gitsizeallow"), []byte("# deliberate dependency fixture\nnode_modules/**\n"))
				writeFile(t, filepath.Join(repo, "node_modules", "pkg", "index.js"), []byte("dependency\n"))
				git(t, repo, "add", ".")
				return "git commit -m integration"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			command := tc.setup(t, repo)
			defer assertGeneratedGitUnchanged(t, repo)()
			sels := []analyzer.Selection{{Dir: repo, Kind: analyzer.KindStaged}}
			baseline, err := analyzer.AnalyzeWithConfig(sels, thresholds, analyzer.NearDupConfig{}, analyzer.GeneratedConfig{})
			if err != nil {
				t.Fatal(err)
			}
			report, err := analyzer.AnalyzeWithConfig(sels, thresholds, analyzer.NearDupConfig{}, generatedCfg)
			if err != nil {
				t.Fatal(err)
			}
			if baseline.RiskLevel != analyzer.RiskLow || report.GrowthBytes != baseline.GrowthBytes || report.GeneratedFiles != tc.count {
				t.Fatalf("report=%+v baseline=%+v", report, baseline)
			}
			wantRisk, wantDecision := analyzer.RiskLow, hookio.Decision("")
			if tc.count > 0 && !tc.weak {
				wantRisk, wantDecision = analyzer.RiskMedium, hookio.Ask
			}
			if report.RiskLevel != wantRisk {
				t.Fatalf("risk=%s want %s", report.RiskLevel, wantRisk)
			}
			if tc.count == 0 {
				if len(report.Reasons) != 0 || len(report.Notes) != 0 || len(report.GeneratedByCategory) != 0 {
					t.Fatalf("expected no advisory: %+v", report)
				}
				for _, file := range report.Files {
					if file.Generated != nil {
						t.Fatalf("unexpected metadata: %+v", file)
					}
				}
			} else {
				if report.GeneratedByCategory[tc.category] != tc.count {
					t.Fatalf("categories=%v", report.GeneratedByCategory)
				}
				var annotated int
				for _, file := range report.Files {
					if g := file.Generated; g != nil {
						annotated++
						if g.Category != tc.category || g.Confidence != tc.confidence || g.IgnoredByGitignore != tc.ignored {
							t.Fatalf("generated=%+v", g)
						}
					}
				}
				if annotated == 0 {
					t.Fatal("missing per-file generated metadata")
				}
				if tc.weak {
					if len(report.Reasons) != 0 || len(report.Notes) != 1 || !strings.Contains(report.Notes[0], "weak evidence") {
						t.Fatalf("weak report=%+v", report)
					}
				} else if len(report.Reasons) != 1 || len(report.Notes) != 0 {
					t.Fatalf("expected one aggregate reason: %+v", report)
				}
			}
			input := hookio.Input{ToolName: "Bash", Cwd: repo, ToolInput: hookio.ToolInput{Command: command}}
			got, reason, growth := evaluateWithConfig(input, thresholds, analyzer.NearDupConfig{}, generatedCfg)
			_, _, baselineGrowth := evaluateWithConfig(input, thresholds, analyzer.NearDupConfig{}, analyzer.GeneratedConfig{})
			if got != wantDecision || growth != baselineGrowth {
				t.Fatalf("decision=%s growth=%d baseline_growth=%d reason=%s", got, growth, baselineGrowth, reason)
			}
			if wantDecision == "" && reason != "" {
				t.Fatalf("unexpected hook output: %s", reason)
			}
			if wantDecision == hookio.Ask && strings.Count(reason, "looks like generated or dependency files") != 1 {
				t.Fatalf("expected one generated sentence: %s", reason)
			}
			if tc.count == 142 {
				needle := fmt.Sprintf("142 files, %s total", analyzer.HumanReadableSize(142*int64(len("export const value = 1;\n"))))
				if !strings.Contains(report.Reasons[0], needle) || !strings.Contains(reason, needle) {
					t.Fatalf("missing aggregate count/size: report=%v hook=%s", report.Reasons, reason)
				}
			}
		})
	}

	t.Run("Write Go convention header comes from payload", func(t *testing.T) {
		repo := newRepo(t)
		writeFile(t, filepath.Join(repo, "generated.go"), []byte("ordinary on-disk content\n"))
		input := hookio.Input{ToolName: "Write", Cwd: repo, ToolInput: hookio.ToolInput{
			FilePath: "generated.go", Content: "// Code generated by x. DO NOT EDIT.\npackage output\n",
		}}
		defer assertGeneratedGitUnchanged(t, repo)()
		report, exact, err := analyzeWriteWithConfig(input.ToolInput, repo, thresholds, analyzer.NearDupConfig{}, generatedCfg)
		if err != nil || !exact || report == nil {
			t.Fatalf("report=%+v exact=%t err=%v", report, exact, err)
		}
		if report.RiskLevel != analyzer.RiskMedium || report.GeneratedFiles != 1 || report.GeneratedByCategory["generated-code"] != 1 || report.GrowthBytes != int64(len(input.ToolInput.Content)) || len(report.Reasons) != 1 || len(report.Notes) != 0 {
			t.Fatalf("report=%+v", report)
		}
		if g := report.Files[0].Generated; g == nil || g.Category != "generated-code" || g.Confidence != "strong" {
			t.Fatalf("metadata=%+v", g)
		}
		decision, reason, growth := evaluateWithConfig(input, thresholds, analyzer.NearDupConfig{}, generatedCfg)
		if decision != hookio.Ask || growth != report.GrowthBytes || !strings.Contains(reason, "generated-code") || strings.Count(reason, "looks like generated or dependency files") != 1 {
			t.Fatalf("decision=%s growth=%d reason=%s", decision, growth, reason)
		}
	})

	t.Run("combined advisories stay medium", func(t *testing.T) {
		repo := newRepo(t)
		original := make([]byte, 12*mib)
		if _, err := rand.New(rand.NewSource(4042026)).Read(original); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(repo, "original.bin"), original)
		git(t, repo, "add", "--", "original.bin")
		git(t, repo, "commit", "-qm", "original")
		modified := bytes.Clone(original)
		start := len(modified) / 2
		for i := start; i < start+len(modified)/20; i++ {
			modified[i] ^= 0xff
		}
		modified = append(append(append([]byte(nil), modified[:start]...), bytes.Repeat([]byte{'x'}, 200)...), modified[start:]...)
		file := filepath.Join("node_modules", "pkg", "copy.bin")
		writeFile(t, filepath.Join(repo, file), modified)
		git(t, repo, "add", "--", file)
		nearCfg := analyzer.DefaultNearDupConfig()
		nearCfg.TimeBudget = time.Minute
		defer assertGeneratedGitUnchanged(t, repo)()
		sels := []analyzer.Selection{{Dir: repo, Kind: analyzer.KindStaged}}
		baseline, err := analyzer.AnalyzeWithConfig(sels, thresholds, analyzer.NearDupConfig{}, analyzer.GeneratedConfig{})
		if err != nil {
			t.Fatal(err)
		}
		report, err := analyzer.AnalyzeWithConfig(sels, thresholds, nearCfg, generatedCfg)
		if err != nil {
			t.Fatal(err)
		}
		if baseline.RiskLevel != analyzer.RiskLow || report.RiskLevel != analyzer.RiskMedium || report.GrowthBytes != baseline.GrowthBytes || report.GrowthBytes != int64(len(modified)) || report.GeneratedFiles != 1 || report.NearDuplicates != 1 || len(report.Reasons) != 2 || len(report.Notes) != 0 {
			t.Fatalf("report=%+v baseline=%+v", report, baseline)
		}
		if dup := report.Files[0].NearDuplicate; dup == nil || dup.Path != "original.bin" || dup.Similarity < .90 {
			t.Fatalf("near duplicate=%+v", dup)
		}
		reasons := strings.Join(report.Reasons, "\n")
		if strings.Count(reasons, "looks like generated or dependency files") != 1 || strings.Count(reasons, "identical to original.bin already in HEAD") != 1 {
			t.Fatalf("reasons=%s", reasons)
		}
		input := hookio.Input{ToolName: "Bash", Cwd: repo, ToolInput: hookio.ToolInput{Command: "git commit -m integration"}}
		decision, reason, growth := evaluateWithConfig(input, thresholds, nearCfg, generatedCfg)
		if decision != hookio.Ask || growth != report.GrowthBytes || strings.Count(reason, "looks like generated or dependency files") != 1 || strings.Count(reason, "identical to original.bin already in HEAD") != 1 {
			t.Fatalf("decision=%s growth=%d reason=%s", decision, growth, reason)
		}
	})
}
