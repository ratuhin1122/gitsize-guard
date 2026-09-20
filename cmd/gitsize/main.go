package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: gitsize <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  analyze   Analyze the size impact of a file on a git repository")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags for 'analyze':")
	fmt.Fprintln(w, "  --repo <path>         Path to the git repository root (required)")
	fmt.Fprintln(w, "  --file <path>         Path to the file being analyzed (required)")
	fmt.Fprintln(w, "  --warning-mb <int>    Warning threshold in MB (optional)")
	fmt.Fprintln(w, "  --failure-mb <int>    Failure threshold in MB (optional)")
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	subcommand := args[0]
	if subcommand != "analyze" {
		fmt.Fprintf(stderr, "unknown subcommand: %q\n", subcommand)
		printUsage(stderr)
		return 2
	}

	defaults := analyzer.DefaultThresholds()
	defaultWarningMB := int(defaults.WarningBytes / (1024 * 1024))
	defaultFailureMB := int(defaults.FailureBytes / (1024 * 1024))

	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoPath := fs.String("repo", "", "Path to the git repository root (required)")
	filePath := fs.String("file", "", "Path to the file being analyzed (required)")
	warningMB := fs.Int("warning-mb", defaultWarningMB, "Warning threshold in MB")
	failureMB := fs.Int("failure-mb", defaultFailureMB, "Failure threshold in MB")

	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}

	if *repoPath == "" || *filePath == "" {
		fmt.Fprintln(stderr, "error: both --repo and --file are required")
		fs.Usage()
		return 2
	}

	targetFile := *filePath
	if !filepath.IsAbs(targetFile) {
		targetFile = filepath.Join(*repoPath, targetFile)
	}

	thresholds := analyzer.Thresholds{
		WarningBytes: int64(*warningMB) * 1024 * 1024,
		FailureBytes: int64(*failureMB) * 1024 * 1024,
	}

	report, err := analyzer.AnalyzeFile(*repoPath, targetFile, thresholds)
	if err != nil {
		fmt.Fprintf(stderr, "analysis error: %v\n", err)
		return 2
	}

	jsonOutput, err := report.ToJSON()
	if err != nil {
		fmt.Fprintf(stderr, "failed to marshal report to JSON: %v\n", err)
		return 2
	}

	fmt.Fprintln(stdout, jsonOutput)

	if report.RiskLevel == analyzer.RiskHigh {
		return 1
	}

	return 0
}
