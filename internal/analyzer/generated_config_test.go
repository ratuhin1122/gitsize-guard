package analyzer

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGeneratedAllowFile(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          []string
		wantErr       bool
	}{
		{"comments and CRLF", "# allowed output\r\n\r\n dist/**  # deliberate\r\nnode_modules/**\r\n", []string{"dist/**", "node_modules/**"}, false},
		{"empty", "", []string{}, false},
		{"invalid glob", "[\ndist/**\n", []string{"dist/**"}, true},
		{"exact cap", strings.Repeat("#", 64<<10), []string{}, false},
		{"over cap", strings.Repeat("#", (64<<10)+1), []string{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo(t)
			writeFile(t, filepath.Join(repo, ".gitsizeallow"), []byte(tc.content))
			defer assertGitFilesUnchanged(t, repo)()
			cfg, err := ResolveGeneratedConfig(repo, DefaultGeneratedConfig())
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(cfg.Allow, tc.want) {
				t.Fatalf("allow=%v err=%v", cfg.Allow, err)
			}
		})
	}
	t.Run("missing optional file", func(t *testing.T) {
		cfg, err := ResolveGeneratedConfig(newRepo(t), DefaultGeneratedConfig())
		if err != nil || len(cfg.Allow) != 0 {
			t.Fatalf("cfg=%+v err=%v", cfg, err)
		}
	})
}

func TestGeneratedAllowSkipsAndStrictWeak(t *testing.T) {
	repo := newRepo(t)
	file := filepath.Join("dist", "app.js")
	writeFile(t, filepath.Join(repo, file), []byte("ordinary"))
	sels := []Selection{{Dir: repo, Kind: KindFiles, Pathspecs: []string{file}}}
	for _, tc := range []struct {
		name         string
		cfg          GeneratedConfig
		risk         string
		count, notes int
	}{
		{"weak", GeneratedConfig{Enabled: true, Allow: []string{}}, RiskLow, 1, 1},
		{"strict weak", GeneratedConfig{Enabled: true, StrictWeak: true, Allow: []string{}}, RiskMedium, 1, 0},
		{"allow beats strict", GeneratedConfig{Enabled: true, StrictWeak: true, Allow: []string{`dist\**`}}, RiskLow, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer assertGitFilesUnchanged(t, repo)()
			r, err := AnalyzeWithConfig(sels, DefaultThresholds(), NearDupConfig{}, tc.cfg)
			if err != nil || r.RiskLevel != tc.risk || r.GeneratedFiles != tc.count || len(r.Notes) != tc.notes || r.GrowthBytes != 8 {
				t.Fatalf("report=%+v err=%v", r, err)
			}
			if tc.count != 0 && r.Files[0].Generated.Confidence != "weak" {
				t.Fatalf("evidence changed: %+v", r.Files[0].Generated)
			}
		})
	}
	writeFile(t, filepath.Join(repo, ".gitsizeallow"), []byte("dist/**\n"))
	r, err := AnalyzeWithConfig(sels, DefaultThresholds(), NearDupConfig{}, DefaultGeneratedConfig())
	if err != nil || r.GeneratedFiles != 0 || len(r.Notes) != 0 || len(r.Reasons) != 0 {
		t.Fatalf("file allow=%+v err=%v", r, err)
	}
	impact := FileImpact{FilePath: filepath.ToSlash(file), SizeBytes: 12, GrowthBytes: 12}
	if notes := CheckGenerated(repo, &impact, []byte("@generated"), DefaultGeneratedConfig()); len(notes) != 0 || impact.Generated != nil {
		t.Fatalf("allowlisted content: %+v notes=%v", impact, notes)
	}
}

func TestGeneratedStagedAddScopeAndGrowth(t *testing.T) {
	repo := newRepo(t)
	writeFile(t, filepath.Join(repo, "node_modules", "a.js"), []byte("dependency"))
	writeFile(t, filepath.Join(repo, "ordinary.txt"), []byte("ordinary"))
	git(t, repo, "add", ".")
	defer assertGitFilesUnchanged(t, repo)()
	for _, tc := range []struct {
		spec  string
		count int
	}{{".", 1}, {"ordinary.txt", 0}, {"node_modules", 1}} {
		r, err := AnalyzeWithConfig([]Selection{{Dir: repo, Kind: KindPaths, Pathspecs: []string{tc.spec}}}, DefaultThresholds(), NearDupConfig{}, DefaultGeneratedConfig())
		if err != nil || r.GeneratedFiles != tc.count || r.GrowthBytes != 0 {
			t.Fatalf("spec=%s report=%+v err=%v", tc.spec, r, err)
		}
		if tc.count == 0 && r.FilesAnalyzed != 0 {
			t.Fatalf("ordinary staged files altered report: %+v", r)
		}
	}
}
