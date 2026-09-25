package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: gitsize <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  analyze   Report the size impact of files on a git repository")
	fmt.Fprintln(w, "  hook      Run as a Claude Code PreToolUse hook (reads the hook payload on stdin)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags for 'analyze':")
	fmt.Fprintln(w, "  --repo <path>         Repository to analyze (default: current directory)")
	fmt.Fprintln(w, "  --file <path>         File to analyze, relative to the current directory; repeatable")
	fmt.Fprintln(w, "  --staged              Analyze the changes already staged in the index")
	fmt.Fprintln(w, "  --pending             Analyze everything `git add -A && git commit` would record")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags for both:")
	fmt.Fprintln(w, "  --warning-mb <int>    Warning threshold in MB (default 50, or $GITSIZE_WARNING_MB)")
	fmt.Fprintln(w, "  --failure-mb <int>    Failure threshold in MB (default 100, or $GITSIZE_FAILURE_MB)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "'analyze' exits 0 for low or medium risk, 1 for high risk, 2 on error.")
	fmt.Fprintln(w, "'hook' always exits 0 and prints a permission decision only when it has one.")
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "analyze":
		return runAnalyze(args[1:], stdout, stderr)
	case "hook":
		return runHook(args[1:], stdin, stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "unknown subcommand: %q\n", args[0])
	printUsage(stderr)
	return 2
}

type fileList []string

func (f *fileList) String() string     { return strings.Join(*f, ",") }
func (f *fileList) Set(v string) error { *f = append(*f, v); return nil }

func runAnalyze(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)

	repoPath := fs.String("repo", ".", "Repository to analyze")
	var files fileList
	fs.Var(&files, "file", "File to analyze, relative to the current directory (repeatable)")
	staged := fs.Bool("staged", false, "Analyze the changes already staged in the index")
	pending := fs.Bool("pending", false, "Analyze everything `git add -A && git commit` would record")
	warningMB, failureMB := thresholdFlags(fs)

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "error: unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}
	if len(files) == 0 && !*staged && !*pending {
		fmt.Fprintln(stderr, "error: nothing to analyze: pass --file, --staged, or --pending")
		fs.Usage()
		return 2
	}

	thresholds, err := resolveThresholds(fs, *warningMB, *failureMB)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 2
	}

	root, err := analyzer.RepoRoot(*repoPath)
	if err != nil {
		fmt.Fprintf(stderr, "analysis error: %s is not inside a git work tree\n", *repoPath)
		return 2
	}

	var sels []analyzer.Selection
	if len(files) > 0 {
		abs := make([]string, len(files))
		for i, f := range files {
			if abs[i], err = filepath.Abs(f); err != nil {
				fmt.Fprintf(stderr, "analysis error: %v\n", err)
				return 2
			}
		}
		sels = append(sels, analyzer.Selection{Dir: root, Kind: analyzer.KindFiles, Pathspecs: abs})
	}
	if *staged {
		sels = append(sels, analyzer.Selection{Dir: root, Kind: analyzer.KindStaged})
	}
	if *pending {
		sels = append(sels, analyzer.Selection{Dir: root, Kind: analyzer.KindPending})
	}

	report, err := analyzer.Analyze(sels, thresholds)
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

// thresholdFlags registers --warning-mb and --failure-mb on fs.
func thresholdFlags(fs *flag.FlagSet) (warningMB, failureMB *int) {
	defaults := analyzer.DefaultThresholds()
	warningMB = fs.Int("warning-mb", int(defaults.WarningBytes/(1024*1024)), "Warning threshold in MB (or $GITSIZE_WARNING_MB)")
	failureMB = fs.Int("failure-mb", int(defaults.FailureBytes/(1024*1024)), "Failure threshold in MB (or $GITSIZE_FAILURE_MB)")
	return warningMB, failureMB
}

// resolveThresholds applies $GITSIZE_WARNING_MB / $GITSIZE_FAILURE_MB for
// flags not given explicitly, then validates the result. When only one limit
// is set, the other is moved to meet it rather than rejecting the pair: a
// stricter failure limit on its own lowers the warning limit too. It always
// returns usable thresholds: on error, invalid values are replaced by their
// defaults, keeping any valid ones.
func resolveThresholds(fs *flag.FlagSet, warningMB, failureMB int) (analyzer.Thresholds, error) {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	warnSet, failSet := set["warning-mb"], set["failure-mb"]

	var errs []error
	if !warnSet {
		if v, ok, err := envMB("GITSIZE_WARNING_MB"); err != nil {
			errs = append(errs, err)
		} else if ok {
			warningMB, warnSet = v, true
		}
	}
	if !failSet {
		if v, ok, err := envMB("GITSIZE_FAILURE_MB"); err != nil {
			errs = append(errs, err)
		} else if ok {
			failureMB, failSet = v, true
		}
	}

	switch {
	case failSet && !warnSet && warningMB > failureMB:
		warningMB = failureMB
	case warnSet && !failSet && warningMB > failureMB:
		failureMB = warningMB
	}
	th, err := analyzer.ThresholdsFromMB(warningMB, failureMB)
	if err != nil {
		errs = append(errs, err)
		th = analyzer.DefaultThresholds()
	}
	return th, errors.Join(errs...)
}

// envMB reads a whole number of megabytes from the environment; ok is false
// when the variable is unset or empty.
func envMB(name string) (n int, ok bool, err error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return 0, false, nil
	}
	n, err = strconv.Atoi(v)
	if err != nil {
		return 0, false, fmt.Errorf("%s=%q is not a whole number of megabytes", name, v)
	}
	return n, true, nil
}
