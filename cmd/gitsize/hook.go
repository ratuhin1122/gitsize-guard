package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ratuhin1122/gitsize-guard/internal/analyzer"
	"github.com/ratuhin1122/gitsize-guard/internal/hookio"
	"github.com/ratuhin1122/gitsize-guard/internal/shellcmd"
)

// maxListed caps the files named in a decision reason.
const maxListed = 5

// runHook handles one Claude Code PreToolUse event. It always exits 0: a
// broken guard must not block unrelated work. It prints a "deny" or "ask"
// decision (see evaluate), or nothing.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(stderr)
	warningMB, failureMB := thresholdFlags(fs)
	if err := fs.Parse(args); err != nil {
		return 0
	}
	thresholds, configErr := resolveThresholds(fs, *warningMB, *failureMB)
	if configErr != nil {
		fmt.Fprintf(stderr, "gitsize-guard: %v\n", configErr)
	}

	in, err := hookio.Read(stdin)
	if err != nil {
		fmt.Fprintf(stderr, "gitsize-guard: %v\n", err)
		return 0
	}

	decision, reason, growth := evaluate(in, thresholds)
	if configErr != nil {
		note := fmt.Sprintf("gitsize-guard configuration error: %v. Using warning %s / failure %s instead.",
			configErr, analyzer.HumanReadableSize(thresholds.WarningBytes), analyzer.HumanReadableSize(thresholds.FailureBytes))
		switch {
		case decision != "":
			reason += "\n(" + note + ")"
		case stagesContent(in):
			decision = hookio.Ask
			reason = fmt.Sprintf("%s This command would add %s to the repository.", note, analyzer.HumanReadableSize(growth))
		}
	}
	if decision != "" {
		if err := hookio.WriteDecision(stdout, decision, reason); err != nil {
			fmt.Fprintf(stderr, "gitsize-guard: %v\n", err)
		}
	}
	return 0
}

// evaluate decides on one tool call, returning the decision, its reason,
// and the exactly measured growth:
//   - deny when the files a command stages, measured exactly, cross the
//     failure threshold;
//   - deny when the command creates or changes files (or the index) before
//     staging them in the same line, since that can't be measured
//     beforehand (Claude can simply run the git step as its own command);
//   - ask when only a conservative estimate crosses a threshold, at the
//     warning threshold, or when part of the command can't be analyzed;
//   - otherwise say nothing.
func evaluate(in hookio.Input, th analyzer.Thresholds) (hookio.Decision, string, int64) {
	cwd := in.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	switch in.ToolName {
	case "Bash":
		home, _ := os.UserHomeDir()
		plan := shellcmd.Parse(in.ToolInput.Command, cwd, home, gitAlias)
		if len(plan.Selections) == 0 && !plan.Unseen && len(plan.Unresolved) == 0 {
			return "", "", 0
		}
		// Measure exact selections apart from estimates ("everything
		// pending"), so an estimate never softens an exact result.
		var exactSels, estSels []analyzer.Selection
		for _, s := range plan.Selections {
			if s.Kind == analyzer.KindPending {
				estSels = append(estSels, s)
			} else {
				exactSels = append(exactSels, s)
			}
		}
		exact, errExact := analyzer.Analyze(exactSels, th)
		est, errEst := analyzer.Analyze(estSels, th)
		err := errors.Join(errExact, errEst)

		var notes []string
		if len(plan.Unresolved) > 0 {
			notes = append(notes, "Not checked: "+strings.Join(plan.Unresolved, "; ")+".")
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("Could not fully check: %v", err))
		}
		withNotes := func(reason string) string {
			if len(notes) == 0 {
				return reason
			}
			return reason + "\n" + strings.Join(notes, "\n")
		}

		switch {
		case exact.RiskLevel == analyzer.RiskHigh:
			return hookio.Deny, withNotes(describe(exact, th, blocked)), exact.GrowthBytes
		case plan.Unseen:
			return hookio.Deny, withNotes(unseenReason(exact, th)), exact.GrowthBytes
		case est.RiskLevel == analyzer.RiskHigh:
			return hookio.Ask, withNotes(describe(est, th, estimated)), exact.GrowthBytes
		case exact.RiskLevel == analyzer.RiskMedium:
			return hookio.Ask, withNotes(describe(exact, th, warning)), exact.GrowthBytes
		case est.RiskLevel == analyzer.RiskMedium:
			return hookio.Ask, withNotes(describe(est, th, estimatedWarning)), exact.GrowthBytes
		case len(notes) > 0:
			return hookio.Ask, "gitsize-guard could not check all of this command's size impact.\n" + strings.Join(notes, "\n"), exact.GrowthBytes
		}
		return "", "", exact.GrowthBytes

	case "Write":
		report, exact, err := analyzeWrite(in.ToolInput, cwd, th)
		switch {
		case err != nil:
			return hookio.Ask, fmt.Sprintf("gitsize-guard could not check how much this file adds to the repository: %v", err), 0
		case report == nil:
		case report.RiskLevel == analyzer.RiskHigh && exact:
			return hookio.Deny, describe(report, th, writeBlocked), report.GrowthBytes
		case report.RiskLevel == analyzer.RiskHigh:
			return hookio.Ask, describe(report, th, writeEstimated), report.GrowthBytes
		case report.RiskLevel == analyzer.RiskMedium:
			return hookio.Ask, describe(report, th, writeWarning), report.GrowthBytes
		}
	}
	return "", "", 0
}

// stagesContent reports whether a Bash call is one the guard checks.
func stagesContent(in hookio.Input) bool {
	if in.ToolName != "Bash" {
		return false
	}
	home, _ := os.UserHomeDir()
	p := shellcmd.Parse(in.ToolInput.Command, in.Cwd, home, gitAlias)
	return len(p.Selections) > 0 || p.Unseen || len(p.Unresolved) > 0
}

func unseenReason(r *analyzer.Report, th analyzer.Thresholds) string {
	var b strings.Builder
	b.WriteString("gitsize-guard blocked this: the command runs something that can create or change files (or the index) " +
		"and then stages or commits with git in the same line, so it can't check their size before the command runs. " +
		"Run the git add / git commit as a separate command after the rest, and gitsize-guard will measure exactly what it stages.")
	if r.GrowthBytes > 0 {
		fmt.Fprintf(&b, "\n(Before this command, it would already add %s; failure threshold %s.)",
			analyzer.HumanReadableSize(r.GrowthBytes), analyzer.HumanReadableSize(th.FailureBytes))
	}
	return b.String()
}

// analyzeWrite checks the content a Write call is about to put in a file.
// Content below the warning threshold costs no git calls at all. exact is
// false when a filter other than Git LFS applies to the path.
func analyzeWrite(ti hookio.ToolInput, cwd string, th analyzer.Thresholds) (*analyzer.Report, bool, error) {
	size := int64(len(ti.Content))
	if risk, _ := analyzer.ClassifyRisk(size, th); risk == analyzer.RiskLow || ti.FilePath == "" {
		return nil, true, nil
	}

	path := ti.FilePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	// The file (and its directories) may not exist yet: find the nearest
	// existing ancestor to locate the repository.
	dir := filepath.Dir(path)
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, true, nil
		}
		dir = parent
	}
	root, err := analyzer.RepoRoot(dir)
	if err != nil {
		return nil, true, nil // not in a repository
	}
	rel, err := relToRoot(root, dir, path)
	if err != nil {
		return nil, true, nil // outside the work tree
	}
	if _, err := analyzer.RunGitCommand(root, "check-ignore", "-q", "--", rel); err == nil {
		return nil, true, nil // ignored: will not be committed
	}
	growth, exact, err := analyzer.WriteGrowth(root, rel, size)
	if err != nil {
		return nil, false, err
	}

	impact := analyzer.FileImpact{
		FilePath:    filepath.ToSlash(rel),
		SizeBytes:   size,
		GrowthBytes: growth,
		IsBinary:    analyzer.ContainsNUL([]byte(ti.Content[:min(len(ti.Content), 8000)])),
		ChangeType:  analyzer.ChangeAdded,
	}
	return analyzer.NewReport([]analyzer.FileImpact{impact}, th), exact, nil
}

// relToRoot returns path relative to root, where dir is an existing ancestor
// of path. Symlinks are resolved on both sides (e.g. /tmp vs /private/tmp).
func relToRoot(root, dir, path string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	suffix, err := filepath.Rel(dir, path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, filepath.Join(realDir, suffix))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside %s", path, root)
	}
	return rel, nil
}

func gitAlias(dir, name string) (string, bool) {
	v, err := analyzer.RunGitCommand(dir, "config", "--get", "alias."+name)
	return v, err == nil && v != ""
}

type verdict int

const (
	blocked          verdict = iota // exact measurement at the failure threshold
	estimated                       // conservative estimate at the failure threshold
	estimatedWarning                // conservative estimate at the warning threshold
	warning                         // exact measurement at the warning threshold
	writeBlocked                    // a Write whose content alone reaches the failure threshold
	writeEstimated                  // ...for a path git filters or re-encodes, so only estimated
	writeWarning                    // a Write at the warning threshold
)

func describe(r *analyzer.Report, th analyzer.Thresholds, v verdict) string {
	var b strings.Builder
	size := analyzer.HumanReadableSize
	switch v {
	case blocked:
		fmt.Fprintf(&b, "gitsize-guard blocked this: it would add %s to the repository (failure threshold %s).",
			size(r.GrowthBytes), size(th.FailureBytes))
	case estimated, estimatedWarning:
		limit, name := th.FailureBytes, "failure"
		if v == estimatedWarning {
			limit, name = th.WarningBytes, "warning"
		}
		fmt.Fprintf(&b, "gitsize-guard can't tell exactly which files this command stages. Everything pending in the repository adds up to %s (%s threshold %s).",
			size(r.GrowthBytes), name, size(limit))
	case warning:
		fmt.Fprintf(&b, "gitsize-guard: this would add %s to the repository (warning threshold %s).",
			size(r.GrowthBytes), size(th.WarningBytes))
	case writeBlocked:
		fmt.Fprintf(&b, "gitsize-guard blocked this: writing this file would add %s to the repository once committed (failure threshold %s).",
			size(r.GrowthBytes), size(th.FailureBytes))
	case writeEstimated:
		fmt.Fprintf(&b, "gitsize-guard: git filters or re-encodes this path, so its stored size isn't known; the content is %s (failure threshold %s).",
			size(r.GrowthBytes), size(th.FailureBytes))
	case writeWarning:
		fmt.Fprintf(&b, "gitsize-guard: writing this file would add %s to the repository once committed (warning threshold %s).",
			size(r.GrowthBytes), size(th.WarningBytes))
	}
	if v == blocked {
		b.WriteString(" This was measured before the command runs, so a fix made earlier in the same command (.gitignore, git lfs track, " +
			"git rm --cached) isn't seen yet: run the fix as its own command, then retry.")
	}
	b.WriteString("\nLargest additions:")
	more := 0
	for i, f := range r.Files {
		if f.GrowthBytes == 0 {
			break // sorted by growth: nothing after this adds anything
		}
		if i >= maxListed {
			more++
			continue
		}
		kind := f.ChangeType
		if f.IsBinary {
			kind += ", binary"
		}
		fmt.Fprintf(&b, "\n  - %s: %s (%s)", f.FilePath, analyzer.HumanReadableSize(f.GrowthBytes), kind)
	}
	if more > 0 {
		plus := ""
		if r.FilesAnalyzed > len(r.Files) {
			plus = "+"
		}
		fmt.Fprintf(&b, "\n  - ...and %d%s more files", more, plus)
	}
	if r.Recommendation != "" {
		fmt.Fprintf(&b, "\nRecommendation: %s", r.Recommendation)
	}
	return b.String()
}
