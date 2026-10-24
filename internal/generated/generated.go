// Package generated classifies paths that commonly contain generated files.
// Classification uses path names only and never reads the filesystem.
package generated

import "strings"

// Confidence describes how specifically a rule identifies generated content.
type Confidence int

const (
	Weak Confidence = iota
	Strong
)

// Match explains which rule classified a path.
type Match struct {
	Category   string
	Confidence Confidence
	Rule       string
}

var strongSegments = map[string]string{
	"node_modules":  "dependencies",
	"__pycache__":   "cache",
	".next":         "build-output",
	".nuxt":         "build-output",
	".gradle":       "cache",
	".pytest_cache": "cache",
	".mypy_cache":   "cache",
	"coverage":      "coverage",
	".nyc_output":   "coverage",
	"htmlcov":       "coverage",
	".terraform":    "dependencies",
}

var weakSegments = map[string]string{
	"dist":   "build-output",
	"build":  "build-output",
	"out":    "build-output",
	"target": "build-output",
	"vendor": "dependencies",
	"bin":    "build-output",
	"obj":    "build-output",
	".cache": "cache",
}

var fileRules = []struct {
	suffix, category string
}{
	{".pyc", "cache"}, {".pyo", "cache"},
	{".class", "build-output"}, {".o", "build-output"},
	{".obj", "build-output"}, {".exe", "build-output"},
	{".dll", "build-output"}, {".so", "build-output"},
	{".dylib", "build-output"}, {".min.js", "build-output"},
	{".min.css", "build-output"}, {".map", "build-output"},
	{".log", "logs"},
}

// Classify normalizes separators and a leading ./ before matching whole path
// segments or strong filename rules. Strong matches take precedence over weak
// matches. Weak rules require a directory segment, not a bare filename; callers
// can identify a directory itself with a trailing slash. Rule is the matched
// segment, filename, or wildcard pattern (for example, node_modules or *.pyc).
func Classify(path string) (Match, bool) {
	path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
	segments := strings.Split(path, "/")
	for _, segment := range segments {
		if category, ok := strongSegments[segment]; ok {
			return Match{category, Strong, segment}, true
		}
	}
	name := segments[len(segments)-1]
	for _, special := range []string{".DS_Store", "Thumbs.db"} {
		if strings.EqualFold(name, special) {
			return Match{"metadata", Strong, special}, true
		}
	}
	for _, rule := range fileRules {
		if strings.HasSuffix(name, rule.suffix) {
			return Match{rule.category, Strong, "*" + rule.suffix}, true
		}
	}
	for _, segment := range segments[:len(segments)-1] {
		if category, ok := weakSegments[segment]; ok {
			return Match{category, Weak, segment}, true
		}
	}
	return Match{}, false
}
