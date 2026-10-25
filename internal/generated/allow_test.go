package generated

import "testing"

func TestMatchGlob(t *testing.T) {
	for _, tc := range []struct {
		pattern, file string
		want          bool
	}{
		{"node_modules/**", "node_modules/pkg/a.js", true},
		{"node_modules/**", `node_modules\pkg\a.js`, true},
		{`src\dist\**`, `src\dist\nested\a.js`, true},
		{"./dist/**", "./dist/a.js", true},
		{"**/dist/**", "src/dist/deep/a.js", true},
		{"**/dist/**", "dist/a.js", true},
		{"dist/", "dist/nested/a.js", true},
		{"dist/*.js", "dist/app.js", true},
		{"dist/*.js", "dist/nested/app.js", false},
		{"dist/?.[jt]s", "dist/a.ts", true},
		{"dist/**", "distribution/app.js", false},
		{"dist/**", "Dist/app.js", false},
		{"**/build/**", "src/rebuild/app.js", false},
		{"**/**/a.js", "a.js", true},
	} {
		t.Run(tc.pattern+"/"+tc.file, func(t *testing.T) {
			got, err := MatchGlob(tc.pattern, tc.file)
			if err != nil || got != tc.want {
				t.Fatalf("MatchGlob(%q,%q)=%v,%v want %v", tc.pattern, tc.file, got, err, tc.want)
			}
		})
	}
	for _, pattern := range []string{"", "[", "foo/[abc", "a\x00b"} {
		if err := ValidateGlob(pattern); err == nil {
			t.Errorf("invalid pattern accepted: %q", pattern)
		}
	}
}
