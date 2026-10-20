package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

type nearDupOptions struct {
	enabled   *bool
	threshold *float64
	minMB     *int
}

func nearDupFlags(fs *flag.FlagSet) nearDupOptions {
	d := analyzer.DefaultNearDupConfig()
	return nearDupOptions{
		enabled:   fs.Bool("near-dup", d.Enabled, "Detect near duplicates (or $GITSIZE_NEARDUP=0/1)"),
		threshold: fs.Float64("near-dup-threshold", d.Threshold, "Minimum similarity, 0.5 to 1.0 (or $GITSIZE_NEARDUP_THRESHOLD)"),
		minMB:     fs.Int("near-dup-min-mb", int(d.MinBytes/(1024*1024)), "Minimum file size in MB, positive (or $GITSIZE_NEARDUP_MIN_MB)"),
	}
}

// Explicit flags override environment values independently. Invalid fields
// fall back to defaults, retaining valid settings and returning all errors.
func resolveNearDup(fs *flag.FlagSet, opts nearDupOptions) (analyzer.NearDupConfig, error) {
	cfg := analyzer.DefaultNearDupConfig()
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var errs []error
	if set["near-dup"] {
		cfg.Enabled = *opts.enabled
	} else if value := strings.TrimSpace(os.Getenv("GITSIZE_NEARDUP")); value != "" {
		switch value {
		case "0":
			cfg.Enabled = false
		case "1":
			cfg.Enabled = true
		default:
			errs = append(errs, fmt.Errorf("GITSIZE_NEARDUP=%q must be 0 or 1", value))
		}
	}
	threshold := *opts.threshold
	if !set["near-dup-threshold"] {
		if value := strings.TrimSpace(os.Getenv("GITSIZE_NEARDUP_THRESHOLD")); value != "" {
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				errs = append(errs, fmt.Errorf("GITSIZE_NEARDUP_THRESHOLD=%q must be a number from 0.5 to 1.0", value))
			} else {
				threshold = parsed
			}
		}
	}
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0.5 || threshold > 1 {
		errs = append(errs, fmt.Errorf("near-dup-threshold (GITSIZE_NEARDUP_THRESHOLD) must be from 0.5 to 1.0"))
	} else {
		cfg.Threshold = threshold
	}
	minMB := *opts.minMB
	if !set["near-dup-min-mb"] {
		if value, ok, err := envMB("GITSIZE_NEARDUP_MIN_MB"); err != nil {
			errs = append(errs, err)
		} else if ok {
			minMB = value
		}
	}
	if minMB <= 0 || minMB > analyzer.MaxThresholdMB {
		errs = append(errs, fmt.Errorf("near-dup-min-mb (GITSIZE_NEARDUP_MIN_MB) must be from 1 to %d MB", analyzer.MaxThresholdMB))
	} else {
		cfg.MinBytes = int64(minMB) * 1024 * 1024
	}
	return cfg, errors.Join(errs...)
}
