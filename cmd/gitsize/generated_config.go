package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
	"github.com/ratuhin1122/gitsize-guard/internal/generated"
)

type generatedOptions struct {
	enabled, strictWeak *bool
	allow               fileList
}

func generatedFlags(fs *flag.FlagSet) *generatedOptions {
	opts := &generatedOptions{}
	opts.enabled = fs.Bool("generated", true, "Detect generated files (or $GITSIZE_GENERATED=0/1)")
	opts.strictWeak = fs.Bool("generated-strict-weak", false, "Ask on weak generated-file matches (or $GITSIZE_GENERATED_STRICT_WEAK=0/1)")
	fs.Var(&opts.allow, "generated-allow", "Allow a generated-file glob (repeatable; overrides environment and .gitsizeallow)")
	return opts
}

func resolveGenerated(fs *flag.FlagSet, opts *generatedOptions) (analyzer.GeneratedConfig, error) {
	cfg := analyzer.DefaultGeneratedConfig()
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var errs []error
	resolveBool := func(flagName, envName string, explicit bool, fallback bool) bool {
		if set[flagName] {
			return explicit
		}
		switch value := strings.TrimSpace(os.Getenv(envName)); value {
		case "":
			return fallback
		case "0":
			return false
		case "1":
			return true
		default:
			errs = append(errs, fmt.Errorf("%s=%q must be 0 or 1", envName, value))
			return fallback
		}
	}
	cfg.Enabled = resolveBool("generated", "GITSIZE_GENERATED", *opts.enabled, true)
	cfg.StrictWeak = resolveBool("generated-strict-weak", "GITSIZE_GENERATED_STRICT_WEAK", *opts.strictWeak, false)
	var patterns []string
	if set["generated-allow"] {
		patterns = opts.allow
	} else if value := strings.TrimSpace(os.Getenv("GITSIZE_GENERATED_ALLOW")); value != "" {
		patterns = strings.Split(value, ",")
	}
	if patterns != nil {
		cfg.Allow = []string{}
		for _, pattern := range patterns {
			pattern = strings.TrimSpace(pattern)
			if err := generated.ValidateGlob(pattern); err != nil {
				errs = append(errs, err)
				continue
			}
			cfg.Allow = append(cfg.Allow, pattern)
		}
	}
	return cfg, errors.Join(errs...)
}
