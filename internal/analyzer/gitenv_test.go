package analyzer

import (
	"os"
	"strings"
	"testing"
)

// TestMain isolates the tests from the developer's git setup: inherited GIT_*
// variables (e.g. GIT_INDEX_FILE when run from a pre-commit hook), global and
// system config (hooksPath, templates, autocrlf), and gitsize's own settings.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "GIT_") || strings.HasPrefix(k, "GITSIZE_") {
			os.Unsetenv(k)
		}
	}
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	os.Exit(m.Run())
}
