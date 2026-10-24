package analyzer

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ratuhin1122/gitsize-guard/internal/generated"
)

const generatedAllowFileLimit = 64 << 10

// ResolveGeneratedConfig loads .gitsizeallow only when Allow is nil. Explicit
// flag/environment lists replace the file list, including its validation.
// Invalid entries are rejected while valid settings remain usable by hooks.
func ResolveGeneratedConfig(repo string, cfg GeneratedConfig) (GeneratedConfig, error) {
	patterns := cfg.Allow
	cfg.Allow = []string{}
	if patterns == nil {
		file := filepath.Join(repo, ".gitsizeallow")
		info, err := os.Lstat(file)
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		if err != nil {
			return cfg, fmt.Errorf(".gitsizeallow: %w", err)
		}
		if !info.Mode().IsRegular() {
			return cfg, fmt.Errorf(".gitsizeallow must be a regular file")
		}
		if info.Size() > generatedAllowFileLimit {
			return cfg, fmt.Errorf(".gitsizeallow exceeds the 64 KiB size cap")
		}
		reader, err := os.Open(file)
		if err != nil {
			return cfg, fmt.Errorf(".gitsizeallow: %w", err)
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, generatedAllowFileLimit+1))
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return cfg, fmt.Errorf(".gitsizeallow: %w", err)
		}
		if len(content) > generatedAllowFileLimit {
			return cfg, fmt.Errorf(".gitsizeallow exceeds the 64 KiB size cap")
		}
		for _, line := range strings.Split(string(content), "\n") {
			line, _, _ = strings.Cut(line, "#")
			if line = strings.TrimSpace(line); line != "" {
				patterns = append(patterns, line)
			}
		}
	}
	var errs []error
	for _, pattern := range patterns {
		if err := generated.ValidateGlob(pattern); err != nil {
			errs = append(errs, err)
			continue
		}
		cfg.Allow = append(cfg.Allow, pattern)
	}
	return cfg, errors.Join(errs...)
}

func (cfg GeneratedConfig) allows(file string) bool {
	for _, pattern := range cfg.Allow {
		if match, _ := generated.MatchGlob(pattern, file); match {
			return true
		}
	}
	return false
}
