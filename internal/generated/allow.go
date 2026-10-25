package generated

import (
	"fmt"
	"path"
	"strings"
)

func normalizeGlobPath(value string) string {
	return strings.TrimPrefix(strings.ReplaceAll(value, "\\", "/"), "./")
}

// ValidateGlob validates a repository-relative glob. A whole ** segment matches
// zero or more directory segments; a trailing slash allows a directory tree.
func ValidateGlob(pattern string) error {
	if pattern == "" || strings.ContainsRune(pattern, 0) {
		return fmt.Errorf("invalid empty or NUL-containing glob")
	}
	for _, segment := range strings.Split(normalizeGlobPath(pattern), "/") {
		if _, err := path.Match(segment, ""); err != nil {
			return fmt.Errorf("invalid glob %q: %w", pattern, err)
		}
	}
	return nil
}

// MatchGlob matches a complete normalized path, case-sensitively. Ordinary *
// and ? do not cross separators. Both paths and patterns accept backslashes.
func MatchGlob(pattern, name string) (bool, error) {
	if err := ValidateGlob(pattern); err != nil {
		return false, err
	}
	pattern = normalizeGlobPath(pattern)
	if strings.HasSuffix(pattern, "/") {
		pattern += "**"
	}
	parts, names := strings.Split(pattern, "/"), strings.Split(normalizeGlobPath(name), "/")
	// Dynamic programming avoids exponential backtracking for repeated **.
	previous := make([]bool, len(names)+1)
	previous[0] = true
	for _, part := range parts {
		next := make([]bool, len(names)+1)
		for j := 0; j <= len(names); j++ {
			if part == "**" {
				next[j] = previous[j] || (j > 0 && next[j-1])
			} else if j > 0 && previous[j-1] {
				next[j], _ = path.Match(part, names[j-1])
			}
		}
		previous = next
	}
	return previous[len(names)], nil
}
