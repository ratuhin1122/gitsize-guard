# VERIFICATION.md — gitsize-guard v0.2.0 (PR #1 Fix Verification)

**Date:** 2026-09-30
**Branch:** `main` (post-merge of PR #1, tag `v0.2.0`)
**Commit:** `6d24a09` (merge commit)
**Platform:** Windows 11 Pro Build 26200, Go 1.27.0, GOOS=windows/GOARCH=amd64
**Binary:** `bin/gitsize.exe` built from merged main with `go build -o bin/gitsize.exe ./cmd/gitsize`
**Method:** Each test feeds a crafted JSON payload directly to `gitsize hook` (the Go binary), which is what the shell shim delegates to. Tests use isolated temporary git repos.

---

## Summary

| # | Claimed Fix | Result |
|---|---|---|
| 1 | Blocked large file stays blocked on retry (no hash-object -w bypass) | ✅ PASS |
| 2 | Consecutive Write/Edit operations don't bloat .git with loose objects | ✅ PASS |
| 3 | git add ., git add -A, git commit -a, git -C all correctly analyzed | ✅ PASS |
| 4 | git add from subdirectory correctly resolves path | ✅ PASS |
| 5 | Path with spaces doesn't cause silent failure | ✅ PASS |
| 6 | Write on brand-new file uses tool_input.content, not "no such file" | ✅ PASS |
| 7 | git commit \<specific-paths\> analyzed against those paths | ✅ PASS |
| 8 | Medium-risk file surfaces ask decision (visible to Claude Code) | ✅ PASS |
| 9 | Analyzer sets GIT_NO_LAZY_FETCH (no network fetch on partial clones) | ✅ PASS |
| 10 | Invalid thresholds rejected or corrected with diagnostic messages | ✅ PASS |
| 11 | Malicious repo binary NOT executed (security fix) | ✅ PASS |

**Result: 11/11 PASS**

---

## Test 1: Blocked large file stays blocked on retry

**Claim:** The old code used `git hash-object -w`, which wrote objects into `.git`. A blocked file could pass on retry because git would see the object as already present. The fix uses `hash-object` without `-w`.

**Setup:** 3 MB binary file, 2 MB failure threshold.

```
Payload: {"tool_name":"Bash","tool_input":{"command":"git add bigfile.bin"},"cwd":"<repo>"}

=== Attempt 1 ===
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",
"permissionDecisionReason":"gitsize-guard blocked this: it would add 3.0 MB to the
repository (failure threshold 2.0 MB). ... bigfile.bin: 3.0 MB (added, binary) ..."}}

=== Attempt 2 (retry, identical payload) ===
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",
"permissionDecisionReason":"gitsize-guard blocked this: it would add 3.0 MB to the
repository (failure threshold 2.0 MB). ... bigfile.bin: 3.0 MB (added, binary) ..."}}
```

**Result: ✅ PASS** — Both attempts return `deny`. The file is not silently cached.

---

## Test 2: Three Write/Edit operations don't bloat .git

**Claim:** The old `hash-object -w` wrote a loose object into `.git/objects` on every Write/Edit check, even when the file was blocked. Three checks on a 3 MB file would add ~9 MB of loose objects.

**Setup:** Three consecutive Write tool calls with 3 MB content each.

```
.git size before: 26,945 bytes
Write attempt 1 — deny=True
Write attempt 2 — deny=True
Write attempt 3 — deny=True
.git size after:  26,945 bytes
Growth: 0 bytes
```

**Result: ✅ PASS** — Zero bytes added to `.git` across three blocked Write operations.

---

## Test 3: git add ., git add -A, git commit -a, git -C all analyzed

**Claim:** The old code only handled simple `git add <file>` commands. The fix parses the full Bash command line to analyze all staging forms.

**Setup:** 3 MB binary file, 2 MB failure threshold.

```
Command: git add .          => DENY
Command: git add -A         => DENY
Command: git -C <path> add  => DENY
```

`git commit -a` is separately verified: it only stages *tracked modified* files (not new untracked ones), so the hook correctly returns no decision for an untracked file but blocks a tracked file modified to exceed the threshold:

```
git commit -a (untracked 3MB file)  => NONE (correct: commit -a ignores untracked)
git commit -a (tracked file modified from small to 4MB) => DENY
git commit -am (shorthand)          => DENY
```

**Result: ✅ PASS** — All staging command forms are correctly intercepted.

---

## Test 4: git add from subdirectory resolves path

**Claim:** The old code didn't resolve relative paths against the payload's `cwd`. Running `git add ../model.bin` from a subdirectory would fail to find the file.

**Setup:** 3 MB file at repo root, payload `cwd` set to `<repo>/subdir`.

```
Command: git add ../model.bin (cwd=<repo>\subdir)
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",
"permissionDecisionReason":"... model.bin: 3.0 MB (added, binary) ..."}}
```

**Result: ✅ PASS** — The relative path `../model.bin` is correctly resolved to the repo root.

---

## Test 5: Path with spaces doesn't cause silent failure

**Claim:** Unquoted paths with spaces could silently disable the hook or cause incorrect file resolution.

**Setup:** File at `my folder/big file.bin` (3 MB), 2 MB failure threshold.

```
Command: git add "my folder/big file.bin"
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",
"permissionDecisionReason":"... my folder/big file.bin: 3.0 MB (added, binary) ..."}}
```

**Result: ✅ PASS** — The quoted path with spaces is correctly parsed and analyzed.

---

## Test 6: Write on brand-new file uses tool_input.content

**Claim:** The old code checked the file on disk. For a Write call, the file doesn't exist yet (Claude Code is about to create it). The fix sizes the `content` field from the tool input.

**Setup:** Write tool call for `brand_new_model.bin` (3 MB content), file does NOT exist on disk.

```
File exists on disk before Write: False
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",
"permissionDecisionReason":"gitsize-guard blocked this: writing this file would add
3.0 MB to the repository once committed (failure threshold 2.0 MB). ...
brand_new_model.bin: 3.0 MB (added) ..."}}
```

**Result: ✅ PASS** — The hook correctly sizes the file from `tool_input.content` without needing the file on disk.

---

## Test 7: git commit \<specific-paths\> analyzed against those paths

**Claim:** `git commit big.bin -m msg` should analyze only `big.bin`, not everything staged.

**Setup:** Two tracked files modified: `big.bin` (3 MB) and `small.txt` (14 bytes). Both have unstaged changes.

```
git commit big.bin -m update-big  => DENY (analyzes big.bin specifically)
git commit small.txt -m update    => NONE (small file, no risk)
git commit -m msg -- big.bin      => DENY (-- syntax also works)
```

**Result: ✅ PASS** — Path-specific commits are analyzed against only the named paths.

---

## Test 8: Medium-risk file surfaces ask decision

**Claim:** The old code only had deny or silent pass. Medium-risk files (between warning and failure thresholds) now surface an `ask` decision visible to Claude Code and the user.

**Setup:** 3 MB file, warning=1 MB, failure=5 MB (file is between thresholds).

```
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",
"permissionDecisionReason":"gitsize-guard: this would add 3.0 MB to the repository
(warning threshold 1.0 MB). ... medium.bin: 3.0 MB (added, binary) ...
Recommendation: Consider Git LFS for medium.bin if it will change often."}}
```

**Result: ✅ PASS** — The `ask` decision with the warning reason is emitted as structured JSON, visible to Claude Code's permission flow.

---

## Test 9: No network fetch on partial/shallow clones

**Claim:** Git commands during analysis could trigger a lazy fetch on partial clones, causing network activity and delays. The fix sets `GIT_NO_LAZY_FETCH=1` on all git subprocesses.

**Evidence — source code:**

```
internal/analyzer/git.go:17: var gitEnv = []string{"GIT_NO_LAZY_FETCH=1", "GIT_OPTIONAL_LOCKS=0"}
```

All git subprocess calls in `internal/analyzer/git.go` use this environment. The hook completed in **422ms** (well under 10s timeout), confirming no network blocking.

**Result: ✅ PASS** — `GIT_NO_LAZY_FETCH=1` is set globally for all analyzer git calls.

---

## Test 10: Invalid thresholds rejected or corrected

**Claim:** The old code silently accepted any threshold value. The fix validates and reports configuration errors.

```
=== Negative GITSIZE_FAILURE_MB=-5 ===
stderr: gitsize-guard: thresholds must not be negative (warning 1 MB, failure -5 MB)
stdout: {"permissionDecision":"ask","permissionDecisionReason":"gitsize-guard
  configuration error: thresholds must not be negative (warning 1 MB, failure -5 MB).
  Using warning 50.0 MB / failure 100.0 MB instead. This command would add 3.0 MB ..."}

=== Non-numeric GITSIZE_FAILURE_MB=banana ===
stderr: gitsize-guard: GITSIZE_FAILURE_MB="banana" is not a whole number of megabytes
stdout: {"permissionDecision":"ask","permissionDecisionReason":"gitsize-guard: this
  would add 3.0 MB ... (gitsize-guard configuration error:
  GITSIZE_FAILURE_MB=\"banana\" is not a whole number of megabytes.
  Using warning 1.0 MB / failure 100.0 MB instead.)"}

=== Inverted (warn=200, fail=1) ===
stderr: gitsize-guard: warning threshold (200 MB) exceeds failure threshold (1 MB)
stdout: {"permissionDecision":"ask","permissionDecisionReason":"gitsize-guard
  configuration error: warning threshold (200 MB) exceeds failure threshold (1 MB).
  Using warning 50.0 MB / failure 100.0 MB instead. ..."}

=== Zero threshold (both=0) ===
stdout: {"permissionDecision":"deny","permissionDecisionReason":"... failure threshold
  0 B ... payload.bin: 3.0 MB (added, binary) ..."}
```

**Result: ✅ PASS** — Negative, non-numeric, and inverted values are diagnosed with clear error messages. Valid zero thresholds are accepted. Invalid values fall back to safe defaults and the error is surfaced in the hook decision.

---

## Test 11: Malicious repo binary NOT executed (security fix)

**Claim:** The old shim fell back to `$REPO_ROOT/bin/gitsize`, meaning a cloned repository could supply a malicious binary for the hook to run. The fix removes this fallback and enforces absolute paths everywhere.

**Shim security analysis (`hooks/pretooluse-gitsize.sh`):**

| Check | Result |
|---|---|
| `is_abs()` function defined | ✅ Yes |
| PATH iterated, only absolute entries kept | ✅ Yes (`_entry` checked with `is_abs`, `_safe` built, `PATH=$_safe`) |
| `GITSIZE_BIN` requires `is_abs_exe` (absolute + executable) | ✅ Yes |
| No `REPO_ROOT/bin/gitsize` fallback | ✅ Yes (removed) |
| `cached_build` uses absolute `CACHE` directory | ✅ Yes |
| `command -v gitsize` fallback requires `is_abs_exe` | ✅ Yes |

**Go binary (`hook.go`) does not reference any repo `bin/` directory** — it IS the binary; it never shells out to another `gitsize`.

**Practical test:** Placed a fake `gitsize.exe` (containing text "MALICIOUS") in a repo's `bin/` directory. No marker file was created — the malicious binary was never executed.

**Result: ✅ PASS** — The shim enforces absolute paths for `GITSIZE_BIN`, drops relative `PATH` entries, and has no repo-local binary fallback.

---

## Conclusion

All 11 claimed fixes from PR #1 have been independently verified against the merged `main` branch with actual command output as evidence. Every test produced the expected behavior. The security model, threshold validation, command parsing, and file analysis all function correctly on Windows.

---

## v0.3.0 verification — 2026-10-20

**Scope:** Automated Go integration tests and CLI validation on Windows/amd64, using `go version go1.27.0 windows/amd64` and `git version 2.44.0.windows.1`. Commands below run from the repository root in PowerShell.

**Not tested in a live Claude Code session.** Hook payload tests exercise the Go entry point; they do not establish that an installed plugin has been exercised interactively in Claude Code.

### Near-duplicate, negative cases, budgets, and repository preservation

The test creates a temporary Git repository and commits a deterministic 12 MiB `math/rand` file (seed `2026`). The modified copy changes a contiguous 5% region and inserts 200 bytes in its middle. An unrelated 12 MiB file uses seed `99`; the small copy is a 1 MiB prefix of the original. The default 5 MiB minimum and 0.90 similarity threshold are used.

The budget case injects a reduced allowance of 25,166,023 bytes into the production check: new-file reading fits, but reading the HEAD candidate would exceed the remainder. This exercises the skip note without allocating a 512 MiB fixture; the production default remains 512 MiB. The test checks that the skip is not returned as an analysis error and does not change growth.

Before and after each analysis, and around the whole sequence, the test compares SHA-256 checksums keyed by relative path for every file under `.git`, including the index, config, refs, reflogs, and objects. This detects created, removed, and changed files, rather than checking only object counts.

The deadline regression test confirms that cancellation interrupts a blocked candidate-stream read. Stream cancellation closes the read pipe as well as killing the launcher, and subprocess waiting is bounded when inherited pipes remain open.

Exact command:

```powershell
go test -count=1 -race -v ./internal/analyzer -run '^TestNearDup(ReadOnlyIntegration|StreamDeadlineInterruptsRead)$'
```

Actual output:

```text
=== RUN   TestNearDupReadOnlyIntegration
=== RUN   TestNearDupReadOnlyIntegration/five_percent_edit_plus_insertion
    neardup_readonly_test.go:136: file=modified-copy.bin size=12583112 growth_bytes=12583112 near_duplicates=1
    neardup_readonly_test.go:146: near_duplicate path=original.bin similarity=0.947235 shared_bytes=11919168 candidate_size=12582912
    neardup_readonly_test.go:147: git_unchanged=true files=29 (SHA-256 of every .git file)
=== RUN   TestNearDupReadOnlyIntegration/unrelated_random_file
    neardup_readonly_test.go:136: file=unrelated.bin size=12582912 growth_bytes=12582912 near_duplicates=0
    neardup_readonly_test.go:147: git_unchanged=true files=29 (SHA-256 of every .git file)
=== RUN   TestNearDupReadOnlyIntegration/one_MiB_copy_below_minimum
    neardup_readonly_test.go:136: file=small-copy.bin size=1048576 growth_bytes=1048576 near_duplicates=0
    neardup_readonly_test.go:147: git_unchanged=true files=29 (SHA-256 of every .git file)
=== RUN   TestNearDupReadOnlyIntegration/candidate_read_exceeds_shared_budget
    neardup_readonly_test.go:177: budget_note="Skipped near-duplicate candidate \"original.bin\": near-duplicate byte budget reached." error=<nil> growth_bytes=12583112
    neardup_readonly_test.go:178: git_unchanged=true files=29 (SHA-256 of every .git file)
=== NAME  TestNearDupReadOnlyIntegration
    neardup_readonly_test.go:179: git_unchanged=true files=29 (SHA-256 of every .git file)
--- PASS: TestNearDupReadOnlyIntegration (14.46s)
    --- PASS: TestNearDupReadOnlyIntegration/five_percent_edit_plus_insertion (3.79s)
    --- PASS: TestNearDupReadOnlyIntegration/unrelated_random_file (3.78s)
    --- PASS: TestNearDupReadOnlyIntegration/one_MiB_copy_below_minimum (0.90s)
    --- PASS: TestNearDupReadOnlyIntegration/candidate_read_exceeds_shared_budget (1.65s)
=== RUN   TestNearDupStreamDeadlineInterruptsRead
--- PASS: TestNearDupStreamDeadlineInterruptsRead (1.01s)
PASS
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	18.030s

```

### Full race suite compared with the pre-change baseline

The pre-change baseline remains outside Git at `$env:TEMP\gitsize-guard-v030-baseline-failures.txt`. The comparison uses leaf test names, excluding parent failures that merely summarize failed subtests. It separately rejects timeouts, panics, and data-race reports. An exit code of 1 from the Go suite is expected when the known Windows baseline failures remain; it must not be described as a clean full-suite pass.

Exact PowerShell commands used for the comparison:

```powershell
$baselinePath = Join-Path $env:TEMP 'gitsize-guard-v030-baseline-failures.txt'
$raceLog = Join-Path $env:TEMP 'gitsize-guard-v030-race.jsonl'
& go test -race -json ./... 2>&1 |
    ForEach-Object { $_.ToString() } |
    Tee-Object -FilePath $raceLog | Out-Null
$raceExit = $LASTEXITCODE
$events = @(Get-Content -LiteralPath $raceLog | ForEach-Object {
    try { $_ | ConvertFrom-Json } catch {}
})
$baseline = @(Get-Content -LiteralPath $baselinePath)
$names = @($events | Where-Object { $_.Action -eq 'fail' -and $_.Test } |
    ForEach-Object { $_.Test } | Sort-Object -Unique)
$leaves = @($names | Where-Object {
    $name = $_
    -not ($names | Where-Object { $_.StartsWith($name + '/') })
})
$newFailures = @($leaves | Where-Object { $_ -notin $baseline })
$panics = @($events | Where-Object { $_.Output -match 'panic:|test timed out|fatal error:' })
$races = @($events | Where-Object { $_.Output -match 'WARNING: DATA RACE' })
$summary = @(
    "Race exit code: $raceExit"
    "Baseline failing leaf tests: $($baseline.Count)"
    "Observed failing leaf tests: $($leaves.Count)"
    "New failing leaf tests: $($newFailures.Count)"
    "Timeouts or panics: $($panics.Count)"
    "Data race reports: $($races.Count)"
)
$newFailures | ForEach-Object { $summary += $_ }
$passed = $newFailures.Count -eq 0 -and $panics.Count -eq 0 -and
    $races.Count -eq 0 -and ($raceExit -eq 0 -or $leaves.Count -gt 0)
if ($passed) { $summary += 'Baseline comparison: PASS' }
else { $summary += 'Baseline comparison: FAIL' }
$summary | Set-Content -LiteralPath (Join-Path $env:TEMP 'gitsize-guard-v030-baseline-summary.txt')
$summary | ForEach-Object { Write-Output $_ }
```
Actual comparison output:

```text
Race exit code: 1
Baseline failing leaf tests: 14
Observed failing leaf tests: 14
New failing leaf tests: 0
Timeouts or panics: 0
Data race reports: 0
Baseline comparison: PASS
```

**Result:** No new failing leaf tests versus the pre-change baseline. The full Windows suite still exits 1 because its 14 known baseline failures remain. No timeouts, panics, or data-race reports were observed in this acceptance run. The earlier timeout and timing-dependent policy-fixture failure were corrected before this run.

### Formatting, vet, and manifest validation

Exact commands:

```powershell
go vet ./...
gofmt -l .
claude plugin validate . --strict
claude plugin validate .claude-plugin/plugin.json --strict
```

`go vet ./...` and `gofmt -l .` both exited 0 with no output. Actual manifest-validation output:

```text
Validating marketplace manifest: C:\Users\FC\Downloads\Claude-Plugin\gitsize-guard\.claude-plugin\marketplace.json

√ Validation passed
Validating plugin manifest: C:\Users\FC\Downloads\Claude-Plugin\gitsize-guard\.claude-plugin\plugin.json

√ Validation passed
```

These results establish automated behavior only. **Live Claude Code session testing has not been performed.**

## v0.3.0 verification update — 2026-10-23

**Not tested in a live Claude Code session.** These are automated Go tests on Windows. The 2026-10-20 passing results above are historical; the latest full race run does **not** satisfy the no-new-failures requirement.

### Current integration output

The existing test still covers the deterministic 12 MiB original, contiguous 5% edit plus 200-byte insertion, unrelated random file, below-minimum 1 MiB copy, byte-budget note, and SHA-256 checksums of every file under `.git`. It has not been regenerated or weakened. The successful near-duplicate result (0.947235 similarity) and unrelated-file negative result above were observed on 2026-10-20; both cases now hit the production five-second time budget. The latest focused run confirms the small-file skip, byte-budget note without an error, unchanged growth, and unchanged contents of all 29 `.git` files.

Exact test command run from the repository root in PowerShell:

```powershell
go test -count=1 -race -v ./internal/analyzer -run '^TestNearDup(ReadOnlyIntegration|StreamDeadlineInterruptsRead)$'
```

Actual output (exit 1):

```text
=== RUN   TestNearDupReadOnlyIntegration
=== RUN   TestNearDupReadOnlyIntegration/five_percent_edit_plus_insertion
    neardup_readonly_test.go:133: unexpected skipped candidates: [Skipped near-duplicate candidate "original.bin": time budget reached.]
    panic.go:694: git_unchanged=true files=29 (SHA-256 of every .git file)
=== RUN   TestNearDupReadOnlyIntegration/unrelated_random_file
    neardup_readonly_test.go:133: unexpected skipped candidates: [Skipped near-duplicate candidate "original.bin": time budget reached.]
    panic.go:694: git_unchanged=true files=29 (SHA-256 of every .git file)
=== RUN   TestNearDupReadOnlyIntegration/one_MiB_copy_below_minimum
    neardup_readonly_test.go:136: file=small-copy.bin size=1048576 growth_bytes=1048576 near_duplicates=0
    neardup_readonly_test.go:147: git_unchanged=true files=29 (SHA-256 of every .git file)
=== RUN   TestNearDupReadOnlyIntegration/candidate_read_exceeds_shared_budget
    neardup_readonly_test.go:177: budget_note="Skipped near-duplicate candidate \"original.bin\": near-duplicate byte budget reached." error=<nil> growth_bytes=12583112
    neardup_readonly_test.go:178: git_unchanged=true files=29 (SHA-256 of every .git file)
=== NAME  TestNearDupReadOnlyIntegration
    neardup_readonly_test.go:179: git_unchanged=true files=29 (SHA-256 of every .git file)
--- FAIL: TestNearDupReadOnlyIntegration (29.06s)
    --- FAIL: TestNearDupReadOnlyIntegration/five_percent_edit_plus_insertion (7.16s)
    --- FAIL: TestNearDupReadOnlyIntegration/unrelated_random_file (6.53s)
    --- PASS: TestNearDupReadOnlyIntegration/one_MiB_copy_below_minimum (1.22s)
    --- PASS: TestNearDupReadOnlyIntegration/candidate_read_exceeds_shared_budget (3.89s)
=== RUN   TestNearDupStreamDeadlineInterruptsRead
--- PASS: TestNearDupStreamDeadlineInterruptsRead (1.01s)
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/internal/analyzer	32.819s
FAIL

```

### Latest full-suite baseline comparison

The immediately preceding full-suite command was:

```powershell
go vet ./...
go test -race ./...
```

Vet exited 0 with no output. The race suite exited 1. Its real output was saved outside Git in `readonly-race-2026-10-23.txt`; the original baseline remains outside Git in `baseline-2026-10-23/baseline-failures.txt`. The following exact PowerShell comparison commands were run against those saved outputs:

```powershell
$verificationRoot = 'C:/Users/FC/.codex/visualizations/2026/10/20/01a15872-8472-7163-93c9-935b36159a26'
$baseline = @(Get-Content "$verificationRoot/baseline-2026-10-23/baseline-failures.txt")
$raceLog = "$verificationRoot/readonly-race-2026-10-23.txt"
$names = @(Select-String -Path $raceLog -Pattern '^\s*--- FAIL: (\S+)' | ForEach-Object { $_.Matches[0].Groups[1].Value } | Sort-Object -Unique)
$leaves = @($names | Where-Object { $testName = $_; -not ($names | Where-Object { $_.StartsWith($testName + '/') }) })
$newFailures = @($leaves | Where-Object { $_ -notin $baseline })
"Baseline failing leaf tests: $($baseline.Count)"
"Observed failing leaf tests: $($leaves.Count)"
"New failing leaf tests: $($newFailures.Count)"
"Data race reports: $(@(Select-String -Path $raceLog -Pattern 'WARNING: DATA RACE').Count)"
$newFailures
if ($newFailures.Count -eq 0) { 'Baseline comparison: PASS' } else { 'Baseline comparison: FAIL' }

```

Actual comparison output:

```text
Baseline failing leaf tests: 14
Observed failing leaf tests: 25
New failing leaf tests: 11
Data race reports: 0
TestBashNearDuplicateAsks
TestHookModifiedTwelveMBNearDuplicate/defaults_ask
TestHookModifiedTwelveMBNearDuplicate/flag_reenables
TestHookModifiedTwelveMBNearDuplicate/high_risk_denies_with_advisory
TestNearDupAnalysisRiskAndGrowth/high_stays_high
TestNearDupAnalysisRiskAndGrowth/low_becomes_medium
TestNearDupAnalysisRiskAndGrowth/medium_stays_medium
TestNearDupReadOnlyIntegration/candidate_read_exceeds_shared_budget
TestNearDupReadOnlyIntegration/five_percent_edit_plus_insertion
TestNearDupReadOnlyIntegration/unrelated_random_file
TestWriteNearDuplicateUsesPayload/low_to_medium_asks
Baseline comparison: FAIL

```

**Result: baseline comparison failed.** There are 11 failing leaf tests absent from the baseline. The failures show time-budget exhaustion, absent hook decisions, or absent near-duplicate advisory text. No data race was reported. The current evidence cannot support a claim of no new test failures, even though the older acceptance run above did. This update changes verification documentation only; the implementation and existing tests are preserved. Live Claude Code session testing remains unperformed.

## v0.3.0 repeated race verification — 2026-10-24

**Not tested in a live Claude Code session.** These are automated Go race tests only; installed-plugin behavior has not been exercised interactively.

Three runs were executed sequentially, with no source edits between runs, on feat/near-duplicate-detection based on commit 430cd25d6e4851e5c7cc61925f2c54337e3c86cc, including the uncommitted per-check time-budget and fixture-isolation changes. Production defaults remain five seconds; the large-file tests explicitly use sixty seconds. SHA-256 checks of the changed Go files before each run confirmed that the tested source stayed unchanged. The previous verification sections are historical results, not the result of these three runs.

The unchanged pre-change baseline was read from the external file C:/Users/FC/.codex/visualizations/2026/10/20/01a15872-8472-7163-93c9-935b36159a26/baseline-2026-10-23/baseline-failures.txt. Parent failures are excluded when failed subtests already represent them. Go suite exit codes are reported separately from comparison against that baseline; a baseline match is not a clean full-suite pass.

Displayed output normalizes line endings and trailing spaces for Markdown; the original captured stdout/stderr logs remain unchanged outside Git.

Environment: go version go1.27.0 windows/amd64; git version 2.44.0.windows.1. Commands ran from the repository root in PowerShell.

### Run 1

Started: 2026-10-24T12:07:59.1858628+06:00. Finished: 2026-10-24T12:10:13.4304476+06:00. Wall time: 134.24 seconds.

Exact command:

~~~powershell
go test -race -count=1 ./...
~~~

Real combined stdout/stderr (exit 1):

~~~text
--- FAIL: TestHookConfigErrorKeepsValidSettings (0.94s)
    hook_test.go:246: want an ask at the kept 1 MB warning threshold with a configuration note; got
--- FAIL: TestHookSurfacesConfigErrors (0.92s)
    main_test.go:264: invalid GITSIZE_FAILURE_MB should be surfaced on git staging commands; got ""
--- FAIL: TestShimNeverRunsRepositoryBinaries (9.90s)
    shim_test.go:121: shim exited 127, want 0
    shim_test.go:127: with no usable binary the shim should ask and say the guard is inactive; got ""
--- FAIL: TestShimUsesGitsizeBinAndFailsOpen (0.58s)
    shim_test.go:139: GITSIZE_BIN: exit 127, output ""; want 0 with the binary's decision
    shim_test.go:147: crashing binary: exit 127, output ""; want 0 and no decision (fail open)
--- FAIL: TestShimFallbackLooksOnlyAtTheCommand (0.88s)
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"git -C \"my dir\" add big.bin"}}: ask=false (output ""), want true
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"cd sub\ngit add big.bin"}}: ask=false (output ""), want true
--- FAIL: TestShimBuildsFromSource (0.86s)
    shim_test.go:218: exit 127, output ""; want a deny naming big.bin
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/cmd/gitsize	117.013s
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	123.718s
?   	github.com/ratuhin1122/gitsize-guard/internal/hookio	[no test files]
--- FAIL: TestSelections (0.04s)
    --- FAIL: TestSelections/--work-tree (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir=/r/.git --work-tree=/r add -A")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
    --- FAIL: TestSelections/--git-dir_of_a_.git_directory (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir /r/.git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/GIT_WORK_TREE_assignment (0.00s)
        shellcmd_test.go:123: Selections("GIT_DIR=/r/.git GIT_WORK_TREE=/r git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_absolute (0.00s)
        shellcmd_test.go:123: Selections("cd /other && git add .")
             got: [{Dir:\work\repo\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_home (0.00s)
        shellcmd_test.go:123: Selections("cd ~/proj; git add x")
             got: [{Dir:\work\repo\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
            want: [{Dir:\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
    --- FAIL: TestSelections/indirect_after_cd (0.00s)
        shellcmd_test.go:123: Selections("cd /other && xargs git add < list.txt")
             got: [{Dir:\work\repo\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
--- FAIL: TestParseUnseen (0.01s)
    shellcmd_test.go:184: Parse("cat > /tmp/msg.txt <<'EOF'\nfix\nEOF\ngit add -A && git commit -F /tmp/msg.txt").Unseen = true, want false
--- FAIL: TestParseUnresolved (0.01s)
    shellcmd_test.go:213: Parse("cd \"$DIR\" && cd /abs && git add .").Unresolved = "git add after a cd whose target can't be determined", want ""
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/internal/shellcmd	1.977s
ok  	github.com/ratuhin1122/gitsize-guard/internal/similarity	12.169s
FAIL
~~~

Failing leaf test names recorded for this run:

~~~text
TestHookConfigErrorKeepsValidSettings
TestHookSurfacesConfigErrors
TestParseUnresolved
TestParseUnseen
TestSelections/--git-dir_of_a_.git_directory
TestSelections/--work-tree
TestSelections/cd_absolute
TestSelections/cd_home
TestSelections/GIT_WORK_TREE_assignment
TestSelections/indirect_after_cd
TestShimBuildsFromSource
TestShimFallbackLooksOnlyAtTheCommand
TestShimNeverRunsRepositoryBinaries
TestShimUsesGitsizeBinAndFailsOpen
~~~

Comparison: baseline names=14; observed failing leaves=14; new failures=0; baseline failures absent=0; data-race reports=0; timeout/panic reports=0.
### Run 2

Started: 2026-10-24T12:10:13.7159986+06:00. Finished: 2026-10-24T12:11:34.7785945+06:00. Wall time: 81.06 seconds.

Exact command:

~~~powershell
go test -race -count=1 ./...
~~~

Real combined stdout/stderr (exit 1):

~~~text
--- FAIL: TestHookConfigErrorKeepsValidSettings (0.45s)
    hook_test.go:246: want an ask at the kept 1 MB warning threshold with a configuration note; got
--- FAIL: TestHookSurfacesConfigErrors (0.43s)
    main_test.go:264: invalid GITSIZE_FAILURE_MB should be surfaced on git staging commands; got ""
--- FAIL: TestShimNeverRunsRepositoryBinaries (7.40s)
    shim_test.go:121: shim exited 127, want 0
    shim_test.go:127: with no usable binary the shim should ask and say the guard is inactive; got ""
--- FAIL: TestShimUsesGitsizeBinAndFailsOpen (0.51s)
    shim_test.go:139: GITSIZE_BIN: exit 127, output ""; want 0 with the binary's decision
    shim_test.go:147: crashing binary: exit 127, output ""; want 0 and no decision (fail open)
--- FAIL: TestShimFallbackLooksOnlyAtTheCommand (1.27s)
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"git -C \"my dir\" add big.bin"}}: ask=false (output ""), want true
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"cd sub\ngit add big.bin"}}: ask=false (output ""), want true
--- FAIL: TestShimBuildsFromSource (1.20s)
    shim_test.go:218: exit 127, output ""; want a deny naming big.bin
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/cmd/gitsize	61.770s
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	71.815s
?   	github.com/ratuhin1122/gitsize-guard/internal/hookio	[no test files]
--- FAIL: TestSelections (0.02s)
    --- FAIL: TestSelections/--work-tree (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir=/r/.git --work-tree=/r add -A")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
    --- FAIL: TestSelections/--git-dir_of_a_.git_directory (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir /r/.git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/GIT_WORK_TREE_assignment (0.00s)
        shellcmd_test.go:123: Selections("GIT_DIR=/r/.git GIT_WORK_TREE=/r git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_absolute (0.00s)
        shellcmd_test.go:123: Selections("cd /other && git add .")
             got: [{Dir:\work\repo\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_home (0.00s)
        shellcmd_test.go:123: Selections("cd ~/proj; git add x")
             got: [{Dir:\work\repo\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
            want: [{Dir:\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
    --- FAIL: TestSelections/indirect_after_cd (0.00s)
        shellcmd_test.go:123: Selections("cd /other && xargs git add < list.txt")
             got: [{Dir:\work\repo\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
--- FAIL: TestParseUnseen (0.01s)
    shellcmd_test.go:184: Parse("cat > /tmp/msg.txt <<'EOF'\nfix\nEOF\ngit add -A && git commit -F /tmp/msg.txt").Unseen = true, want false
--- FAIL: TestParseUnresolved (0.00s)
    shellcmd_test.go:213: Parse("cd \"$DIR\" && cd /abs && git add .").Unresolved = "git add after a cd whose target can't be determined", want ""
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/internal/shellcmd	1.590s
ok  	github.com/ratuhin1122/gitsize-guard/internal/similarity	8.391s
FAIL
~~~

Failing leaf test names recorded for this run:

~~~text
TestHookConfigErrorKeepsValidSettings
TestHookSurfacesConfigErrors
TestParseUnresolved
TestParseUnseen
TestSelections/--git-dir_of_a_.git_directory
TestSelections/--work-tree
TestSelections/cd_absolute
TestSelections/cd_home
TestSelections/GIT_WORK_TREE_assignment
TestSelections/indirect_after_cd
TestShimBuildsFromSource
TestShimFallbackLooksOnlyAtTheCommand
TestShimNeverRunsRepositoryBinaries
TestShimUsesGitsizeBinAndFailsOpen
~~~

Comparison: baseline names=14; observed failing leaves=14; new failures=0; baseline failures absent=0; data-race reports=0; timeout/panic reports=0.
### Run 3

Started: 2026-10-24T12:11:34.9065446+06:00. Finished: 2026-10-24T12:13:03.4342196+06:00. Wall time: 88.53 seconds.

Exact command:

~~~powershell
go test -race -count=1 ./...
~~~

Real combined stdout/stderr (exit 1):

~~~text
--- FAIL: TestHookConfigErrorKeepsValidSettings (0.48s)
    hook_test.go:246: want an ask at the kept 1 MB warning threshold with a configuration note; got
--- FAIL: TestHookSurfacesConfigErrors (0.51s)
    main_test.go:264: invalid GITSIZE_FAILURE_MB should be surfaced on git staging commands; got ""
--- FAIL: TestShimNeverRunsRepositoryBinaries (9.97s)
    shim_test.go:121: shim exited 127, want 0
    shim_test.go:127: with no usable binary the shim should ask and say the guard is inactive; got ""
--- FAIL: TestShimUsesGitsizeBinAndFailsOpen (1.09s)
    shim_test.go:139: GITSIZE_BIN: exit 127, output ""; want 0 with the binary's decision
    shim_test.go:147: crashing binary: exit 127, output ""; want 0 and no decision (fail open)
--- FAIL: TestShimFallbackLooksOnlyAtTheCommand (1.37s)
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"git -C \"my dir\" add big.bin"}}: ask=false (output ""), want true
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"cd sub\ngit add big.bin"}}: ask=false (output ""), want true
--- FAIL: TestShimBuildsFromSource (1.23s)
    shim_test.go:218: exit 127, output ""; want a deny naming big.bin
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/cmd/gitsize	72.567s
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	80.828s
?   	github.com/ratuhin1122/gitsize-guard/internal/hookio	[no test files]
--- FAIL: TestSelections (0.03s)
    --- FAIL: TestSelections/--work-tree (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir=/r/.git --work-tree=/r add -A")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
    --- FAIL: TestSelections/--git-dir_of_a_.git_directory (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir /r/.git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/GIT_WORK_TREE_assignment (0.00s)
        shellcmd_test.go:123: Selections("GIT_DIR=/r/.git GIT_WORK_TREE=/r git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_absolute (0.00s)
        shellcmd_test.go:123: Selections("cd /other && git add .")
             got: [{Dir:\work\repo\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_home (0.00s)
        shellcmd_test.go:123: Selections("cd ~/proj; git add x")
             got: [{Dir:\work\repo\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
            want: [{Dir:\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
    --- FAIL: TestSelections/indirect_after_cd (0.00s)
        shellcmd_test.go:123: Selections("cd /other && xargs git add < list.txt")
             got: [{Dir:\work\repo\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
--- FAIL: TestParseUnseen (0.01s)
    shellcmd_test.go:184: Parse("cat > /tmp/msg.txt <<'EOF'\nfix\nEOF\ngit add -A && git commit -F /tmp/msg.txt").Unseen = true, want false
--- FAIL: TestParseUnresolved (0.00s)
    shellcmd_test.go:213: Parse("cd \"$DIR\" && cd /abs && git add .").Unresolved = "git add after a cd whose target can't be determined", want ""
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/internal/shellcmd	1.507s
ok  	github.com/ratuhin1122/gitsize-guard/internal/similarity	8.918s
FAIL
~~~

Failing leaf test names recorded for this run:

~~~text
TestHookConfigErrorKeepsValidSettings
TestHookSurfacesConfigErrors
TestParseUnresolved
TestParseUnseen
TestSelections/--git-dir_of_a_.git_directory
TestSelections/--work-tree
TestSelections/cd_absolute
TestSelections/cd_home
TestSelections/GIT_WORK_TREE_assignment
TestSelections/indirect_after_cd
TestShimBuildsFromSource
TestShimFallbackLooksOnlyAtTheCommand
TestShimNeverRunsRepositoryBinaries
TestShimUsesGitsizeBinAndFailsOpen
~~~

Comparison: baseline names=14; observed failing leaves=14; new failures=0; baseline failures absent=0; data-race reports=0; timeout/panic reports=0.

### Three-run comparison

| Run | Go exit | Failing leaves | New vs baseline | Baseline names absent | Data races | Timeouts/panics |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 1 | 14 | 0 | 0 | 0 | 0 |
| 2 | 1 | 14 | 0 | 0 | 0 | 0 |
| 3 | 1 | 14 | 0 | 0 | 0 | 0 |

**Result:** All three runs exited 1 with exactly the same 14 known baseline failing leaf tests and no new failures, data-race reports, or timeout/panic reports. This is a match to the baseline, not a clean full-suite pass.

Full logs, per-run failure-name files, the orchestration script, and results.json remain outside Git in C:\Users\FC\.codex\visualizations\2026\10\20\01a15872-8472-7163-93c9-935b36159a26\triple-race-budget-2026-10-24. The baseline file was not modified or committed. **Live Claude Code session testing has not been performed.**

## v0.4.0 verification — 2026-10-24

**This version has not been tested in a live Claude Code session.** These are Go tests, simulated hook payloads, and plugin-manifest checks. Markdown output blocks normalize line endings and trailing blanks only; recorded messages and timings are unchanged, and the original raw logs remain outside Git. No live recording or interactive Claude Code behavior is claimed.

The generated-file implementation and test-budget changes were exercised by the three consecutive full runs recorded below; their code is now committed on this branch through `3c45c38`. This documentation/version update changes no Go behavior. Production generated-file defaults remain **5 seconds / 5000 paths**. Large-tree and 12 MiB integration tests inject a 60-second budget; dedicated tests retain tiny-budget skip assertions. No assertions were weakened or deleted, and `baseline-failures-v0.4.txt` was neither edited nor committed.

### Fresh generated-file integration and budget verification

Exact command:

~~~powershell
go test -race -count=1 ./cmd/gitsize ./internal/analyzer -run '^(TestGeneratedReadOnlyIntegration|TestGeneratedBudgetDefaultsAndOverrides|TestGeneratedDefaultPathCap|TestGeneratedTinyTimeBudgetSkipsWithNote|TestGeneratedHookInjectedLimits|TestGeneratedHookTinyBudgetFailsOpen)$' -v
~~~

Exit `0`. Passing assertions cover one aggregated reason and an `ask` for 142 staged dependency files; a weak `dist/` note without risk escalation; HEAD-tracked-folder suppression; ignored force-adds becoming strong; the Go generated header supplied in a Write payload; silent allowlisting; both generated and near-duplicate reasons while risk remains medium; unchanged growth; and SHA-256 equality of every file under `.git` before and after analysis. The integration fixture for the combined advisories commits a deterministic 12 MiB original, changes a contiguous 5% region, and inserts 200 bytes into the copy.

<details>
<summary>Real combined stdout/stderr from the fresh focused run</summary>

~~~text
=== RUN   TestGeneratedHookInjectedLimits
=== RUN   TestGeneratedHookInjectedLimits/large_tree_generous_deadline
=== RUN   TestGeneratedHookInjectedLimits/injected_path_cap
=== NAME  TestGeneratedHookInjectedLimits
    generated_budget_test.go:50: git_unchanged=true files=27 (SHA-256 of every .git file)
--- PASS: TestGeneratedHookInjectedLimits (4.88s)
    --- PASS: TestGeneratedHookInjectedLimits/large_tree_generous_deadline (1.20s)
    --- PASS: TestGeneratedHookInjectedLimits/injected_path_cap (1.55s)
=== RUN   TestGeneratedHookTinyBudgetFailsOpen
    generated_budget_test.go:81: exhausted generated budget: exit=0 stdout="" stderr=""; CLI skip note="Generated-file check skipped: time budget reached."
    generated_budget_test.go:82: git_unchanged=true files=26 (SHA-256 of every .git file)
--- PASS: TestGeneratedHookTinyBudgetFailsOpen (4.95s)
=== RUN   TestGeneratedReadOnlyIntegration
=== RUN   TestGeneratedReadOnlyIntegration/many_staged_dependencies_aggregate_once
    generated_integration_test.go:228: git_unchanged=true files=27 (SHA-256 of every .git file)
=== RUN   TestGeneratedReadOnlyIntegration/new_staged_dist_is_weak_note_only
    generated_integration_test.go:228: git_unchanged=true files=27 (SHA-256 of every .git file)
=== RUN   TestGeneratedReadOnlyIntegration/HEAD_tracked_dist_is_suppressed
    generated_integration_test.go:228: git_unchanged=true files=31 (SHA-256 of every .git file)
=== RUN   TestGeneratedReadOnlyIntegration/force_added_ignored_path_is_strong
    generated_integration_test.go:228: git_unchanged=true files=28 (SHA-256 of every .git file)
=== RUN   TestGeneratedReadOnlyIntegration/allowlisted_staged_dependencies_are_silent
    generated_integration_test.go:228: git_unchanged=true files=28 (SHA-256 of every .git file)
=== RUN   TestGeneratedReadOnlyIntegration/Write_Go_convention_header_comes_from_payload
    generated_integration_test.go:252: git_unchanged=true files=26 (SHA-256 of every .git file)
=== RUN   TestGeneratedReadOnlyIntegration/combined_advisories_stay_medium
    generated_integration_test.go:299: git_unchanged=true files=30 (SHA-256 of every .git file)
--- PASS: TestGeneratedReadOnlyIntegration (51.28s)
    --- PASS: TestGeneratedReadOnlyIntegration/many_staged_dependencies_aggregate_once (6.63s)
    --- PASS: TestGeneratedReadOnlyIntegration/new_staged_dist_is_weak_note_only (6.03s)
    --- PASS: TestGeneratedReadOnlyIntegration/HEAD_tracked_dist_is_suppressed (6.28s)
    --- PASS: TestGeneratedReadOnlyIntegration/force_added_ignored_path_is_strong (6.84s)
    --- PASS: TestGeneratedReadOnlyIntegration/allowlisted_staged_dependencies_are_silent (6.22s)
    --- PASS: TestGeneratedReadOnlyIntegration/Write_Go_convention_header_comes_from_payload (4.17s)
    --- PASS: TestGeneratedReadOnlyIntegration/combined_advisories_stay_medium (15.10s)
PASS
ok  	github.com/ratuhin1122/gitsize-guard/cmd/gitsize	64.274s
=== RUN   TestGeneratedDefaultPathCap
    generated_test.go:148: git_unchanged=true files=26 (SHA-256 of every .git file)
--- PASS: TestGeneratedDefaultPathCap (1.78s)
=== RUN   TestGeneratedBudgetDefaultsAndOverrides
=== RUN   TestGeneratedBudgetDefaultsAndOverrides/production_defaults
=== RUN   TestGeneratedBudgetDefaultsAndOverrides/negative_values_use_defaults
=== RUN   TestGeneratedBudgetDefaultsAndOverrides/injected_limits
--- PASS: TestGeneratedBudgetDefaultsAndOverrides (0.00s)
    --- PASS: TestGeneratedBudgetDefaultsAndOverrides/production_defaults (0.00s)
    --- PASS: TestGeneratedBudgetDefaultsAndOverrides/negative_values_use_defaults (0.00s)
    --- PASS: TestGeneratedBudgetDefaultsAndOverrides/injected_limits (0.00s)
=== RUN   TestGeneratedTinyTimeBudgetSkipsWithNote
    generated_test.go:198: git_unchanged=true files=26 (SHA-256 of every .git file)
--- PASS: TestGeneratedTinyTimeBudgetSkipsWithNote (2.96s)
PASS
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	7.698s
~~~

</details>

### Separate fail-open finding (unchanged)

The fresh `TestGeneratedHookTinyBudgetFailsOpen` output above records:

~~~text
    generated_budget_test.go:81: exhausted generated budget: exit=0 stdout="" stderr=""; CLI skip note="Generated-file check skipped: time budget reached."
~~~

This deliberately exhausts the generated-file budget; it does not establish the cause of earlier intermittent failures. A skip note alone does not create a hook permission decision while measured size risk remains low. In `cmd/gitsize/hook.go`, Bash can return an empty decision, `runHook` writes JSON only for nonempty decisions, and the runner returns `0`. The shell shim in `hooks/pretooluse-gitsize.sh` also prints only successful nonempty binary output and then exits `0`. These fail-open decisions were not changed by the budget injection work; the limitation is documented in the README.

### Three consecutive uncached full race runs

These are the original three consecutive runs from the preceding verification step, with no intervening code edits or retries. They are not re-created or described as clean passes. Each ran exactly:

~~~powershell
go test -race -count=1 ./...
~~~

| Run | Go exit | Failing leaf tests | New vs v0.4 baseline | Race/panic/timeout/build markers |
| --- | --- | --- | --- | --- |
| 1 | 1 | 14 | 0 | 0 |
| 2 | 1 | 14 | 0 | 0 |
| 3 | 1 | 14 | 0 | 0 |

#### Full run 1

Started (UTC): `2026-10-24T14:11:49.2675570Z`. Finished (UTC): `2026-10-24T14:18:21.7389688Z`. Exit: `1`.

~~~powershell
go test -race -count=1 ./...
~~~

<details>
<summary>Run 1: exact combined stdout/stderr and failing leaf names</summary>

~~~text
--- FAIL: TestHookConfigErrorKeepsValidSettings (1.53s)
    hook_test.go:246: want an ask at the kept 1 MB warning threshold with a configuration note; got
--- FAIL: TestHookSurfacesConfigErrors (1.20s)
    main_test.go:264: invalid GITSIZE_FAILURE_MB should be surfaced on git staging commands; got ""
--- FAIL: TestShimNeverRunsRepositoryBinaries (13.76s)
    shim_test.go:121: shim exited 127, want 0
    shim_test.go:127: with no usable binary the shim should ask and say the guard is inactive; got ""
--- FAIL: TestShimUsesGitsizeBinAndFailsOpen (0.69s)
    shim_test.go:139: GITSIZE_BIN: exit 127, output ""; want 0 with the binary's decision
    shim_test.go:147: crashing binary: exit 127, output ""; want 0 and no decision (fail open)
--- FAIL: TestShimFallbackLooksOnlyAtTheCommand (1.41s)
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"git -C \"my dir\" add big.bin"}}: ask=false (output ""), want true
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"cd sub\ngit add big.bin"}}: ask=false (output ""), want true
--- FAIL: TestShimBuildsFromSource (1.86s)
    shim_test.go:218: exit 127, output ""; want a deny naming big.bin
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/cmd/gitsize	377.879s
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	306.594s
ok  	github.com/ratuhin1122/gitsize-guard/internal/generated	1.703s
?   	github.com/ratuhin1122/gitsize-guard/internal/hookio	[no test files]
--- FAIL: TestSelections (0.03s)
    --- FAIL: TestSelections/--work-tree (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir=/r/.git --work-tree=/r add -A")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
    --- FAIL: TestSelections/--git-dir_of_a_.git_directory (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir /r/.git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/GIT_WORK_TREE_assignment (0.00s)
        shellcmd_test.go:123: Selections("GIT_DIR=/r/.git GIT_WORK_TREE=/r git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_absolute (0.00s)
        shellcmd_test.go:123: Selections("cd /other && git add .")
             got: [{Dir:\work\repo\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_home (0.00s)
        shellcmd_test.go:123: Selections("cd ~/proj; git add x")
             got: [{Dir:\work\repo\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
            want: [{Dir:\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
    --- FAIL: TestSelections/indirect_after_cd (0.00s)
        shellcmd_test.go:123: Selections("cd /other && xargs git add < list.txt")
             got: [{Dir:\work\repo\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
--- FAIL: TestParseUnseen (0.02s)
    shellcmd_test.go:184: Parse("cat > /tmp/msg.txt <<'EOF'\nfix\nEOF\ngit add -A && git commit -F /tmp/msg.txt").Unseen = true, want false
--- FAIL: TestParseUnresolved (0.00s)
    shellcmd_test.go:213: Parse("cd \"$DIR\" && cd /abs && git add .").Unresolved = "git add after a cd whose target can't be determined", want ""
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/internal/shellcmd	2.315s
ok  	github.com/ratuhin1122/gitsize-guard/internal/similarity	11.940s
FAIL
~~~

Failing leaf test names extracted from this run:

~~~text
TestHookConfigErrorKeepsValidSettings
TestHookSurfacesConfigErrors
TestParseUnresolved
TestParseUnseen
TestSelections/--git-dir_of_a_.git_directory
TestSelections/--work-tree
TestSelections/cd_absolute
TestSelections/cd_home
TestSelections/GIT_WORK_TREE_assignment
TestSelections/indirect_after_cd
TestShimBuildsFromSource
TestShimFallbackLooksOnlyAtTheCommand
TestShimNeverRunsRepositoryBinaries
TestShimUsesGitsizeBinAndFailsOpen
~~~

</details>

#### Full run 2

Started (UTC): `2026-10-24T14:18:21.8362211Z`. Finished (UTC): `2026-10-24T14:25:02.5075642Z`. Exit: `1`.

~~~powershell
go test -race -count=1 ./...
~~~

<details>
<summary>Run 2: exact combined stdout/stderr and failing leaf names</summary>

~~~text
--- FAIL: TestHookConfigErrorKeepsValidSettings (1.34s)
    hook_test.go:246: want an ask at the kept 1 MB warning threshold with a configuration note; got
--- FAIL: TestHookSurfacesConfigErrors (1.53s)
    main_test.go:264: invalid GITSIZE_FAILURE_MB should be surfaced on git staging commands; got ""
--- FAIL: TestShimNeverRunsRepositoryBinaries (13.79s)
    shim_test.go:121: shim exited 127, want 0
    shim_test.go:127: with no usable binary the shim should ask and say the guard is inactive; got ""
--- FAIL: TestShimUsesGitsizeBinAndFailsOpen (0.95s)
    shim_test.go:139: GITSIZE_BIN: exit 127, output ""; want 0 with the binary's decision
    shim_test.go:147: crashing binary: exit 127, output ""; want 0 and no decision (fail open)
--- FAIL: TestShimFallbackLooksOnlyAtTheCommand (2.39s)
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"git -C \"my dir\" add big.bin"}}: ask=false (output ""), want true
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"cd sub\ngit add big.bin"}}: ask=false (output ""), want true
--- FAIL: TestShimBuildsFromSource (2.52s)
    shim_test.go:218: exit 127, output ""; want a deny naming big.bin
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/cmd/gitsize	385.307s
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	310.292s
ok  	github.com/ratuhin1122/gitsize-guard/internal/generated	1.937s
?   	github.com/ratuhin1122/gitsize-guard/internal/hookio	[no test files]
--- FAIL: TestSelections (0.03s)
    --- FAIL: TestSelections/--work-tree (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir=/r/.git --work-tree=/r add -A")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
    --- FAIL: TestSelections/--git-dir_of_a_.git_directory (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir /r/.git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/GIT_WORK_TREE_assignment (0.00s)
        shellcmd_test.go:123: Selections("GIT_DIR=/r/.git GIT_WORK_TREE=/r git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_absolute (0.00s)
        shellcmd_test.go:123: Selections("cd /other && git add .")
             got: [{Dir:\work\repo\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_home (0.00s)
        shellcmd_test.go:123: Selections("cd ~/proj; git add x")
             got: [{Dir:\work\repo\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
            want: [{Dir:\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
    --- FAIL: TestSelections/indirect_after_cd (0.00s)
        shellcmd_test.go:123: Selections("cd /other && xargs git add < list.txt")
             got: [{Dir:\work\repo\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
--- FAIL: TestParseUnseen (0.01s)
    shellcmd_test.go:184: Parse("cat > /tmp/msg.txt <<'EOF'\nfix\nEOF\ngit add -A && git commit -F /tmp/msg.txt").Unseen = true, want false
--- FAIL: TestParseUnresolved (0.00s)
    shellcmd_test.go:213: Parse("cd \"$DIR\" && cd /abs && git add .").Unresolved = "git add after a cd whose target can't be determined", want ""
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/internal/shellcmd	2.067s
ok  	github.com/ratuhin1122/gitsize-guard/internal/similarity	12.805s
FAIL
~~~

Failing leaf test names extracted from this run:

~~~text
TestHookConfigErrorKeepsValidSettings
TestHookSurfacesConfigErrors
TestParseUnresolved
TestParseUnseen
TestSelections/--git-dir_of_a_.git_directory
TestSelections/--work-tree
TestSelections/cd_absolute
TestSelections/cd_home
TestSelections/GIT_WORK_TREE_assignment
TestSelections/indirect_after_cd
TestShimBuildsFromSource
TestShimFallbackLooksOnlyAtTheCommand
TestShimNeverRunsRepositoryBinaries
TestShimUsesGitsizeBinAndFailsOpen
~~~

</details>

#### Full run 3

Started (UTC): `2026-10-24T14:25:02.5301124Z`. Finished (UTC): `2026-10-24T14:31:40.2712648Z`. Exit: `1`.

~~~powershell
go test -race -count=1 ./...
~~~

<details>
<summary>Run 3: exact combined stdout/stderr and failing leaf names</summary>

~~~text
--- FAIL: TestHookConfigErrorKeepsValidSettings (1.32s)
    hook_test.go:246: want an ask at the kept 1 MB warning threshold with a configuration note; got
--- FAIL: TestHookSurfacesConfigErrors (1.29s)
    main_test.go:264: invalid GITSIZE_FAILURE_MB should be surfaced on git staging commands; got ""
--- FAIL: TestShimNeverRunsRepositoryBinaries (14.00s)
    shim_test.go:121: shim exited 127, want 0
    shim_test.go:127: with no usable binary the shim should ask and say the guard is inactive; got ""
--- FAIL: TestShimUsesGitsizeBinAndFailsOpen (0.95s)
    shim_test.go:139: GITSIZE_BIN: exit 127, output ""; want 0 with the binary's decision
    shim_test.go:147: crashing binary: exit 127, output ""; want 0 and no decision (fail open)
--- FAIL: TestShimFallbackLooksOnlyAtTheCommand (1.97s)
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"git -C \"my dir\" add big.bin"}}: ask=false (output ""), want true
    shim_test.go:173: payload {"tool_name":"Bash","tool_input":{"command":"cd sub\ngit add big.bin"}}: ask=false (output ""), want true
--- FAIL: TestShimBuildsFromSource (2.03s)
    shim_test.go:218: exit 127, output ""; want a deny naming big.bin
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/cmd/gitsize	383.782s
ok  	github.com/ratuhin1122/gitsize-guard/internal/analyzer	308.615s
ok  	github.com/ratuhin1122/gitsize-guard/internal/generated	1.986s
?   	github.com/ratuhin1122/gitsize-guard/internal/hookio	[no test files]
--- FAIL: TestSelections (0.03s)
    --- FAIL: TestSelections/--work-tree (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir=/r/.git --work-tree=/r add -A")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[:/] Force:false Renormalize:false}]
    --- FAIL: TestSelections/--git-dir_of_a_.git_directory (0.00s)
        shellcmd_test.go:123: Selections("git --git-dir /r/.git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/GIT_WORK_TREE_assignment (0.00s)
        shellcmd_test.go:123: Selections("GIT_DIR=/r/.git GIT_WORK_TREE=/r git add y")
             got: [{Dir:\work\repo\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
            want: [{Dir:\r Kind:0 Pathspecs:[y] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_absolute (0.00s)
        shellcmd_test.go:123: Selections("cd /other && git add .")
             got: [{Dir:\work\repo\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:0 Pathspecs:[.] Force:false Renormalize:false}]
    --- FAIL: TestSelections/cd_home (0.00s)
        shellcmd_test.go:123: Selections("cd ~/proj; git add x")
             got: [{Dir:\work\repo\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
            want: [{Dir:\home\me\proj Kind:0 Pathspecs:[x] Force:false Renormalize:false}]
    --- FAIL: TestSelections/indirect_after_cd (0.00s)
        shellcmd_test.go:123: Selections("cd /other && xargs git add < list.txt")
             got: [{Dir:\work\repo\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
            want: [{Dir:\other Kind:3 Pathspecs:[] Force:false Renormalize:false}]
--- FAIL: TestParseUnseen (0.01s)
    shellcmd_test.go:184: Parse("cat > /tmp/msg.txt <<'EOF'\nfix\nEOF\ngit add -A && git commit -F /tmp/msg.txt").Unseen = true, want false
--- FAIL: TestParseUnresolved (0.00s)
    shellcmd_test.go:213: Parse("cd \"$DIR\" && cd /abs && git add .").Unresolved = "git add after a cd whose target can't be determined", want ""
FAIL
FAIL	github.com/ratuhin1122/gitsize-guard/internal/shellcmd	2.524s
ok  	github.com/ratuhin1122/gitsize-guard/internal/similarity	14.059s
FAIL
~~~

Failing leaf test names extracted from this run:

~~~text
TestHookConfigErrorKeepsValidSettings
TestHookSurfacesConfigErrors
TestParseUnresolved
TestParseUnseen
TestSelections/--git-dir_of_a_.git_directory
TestSelections/--work-tree
TestSelections/cd_absolute
TestSelections/cd_home
TestSelections/GIT_WORK_TREE_assignment
TestSelections/indirect_after_cd
TestShimBuildsFromSource
TestShimFallbackLooksOnlyAtTheCommand
TestShimNeverRunsRepositoryBinaries
TestShimUsesGitsizeBinAndFailsOpen
~~~

</details>

### Baseline comparison and integrity

The following PowerShell commands were run against the saved outputs and the baseline outside Git. They exclude parent tests when failing subtests exist and compare the remaining leaf names.

<details>
<summary>Exact comparison commands</summary>

~~~powershell
$evidenceDir='C:\Users\FC\.codex\visualizations\2026\10\20\01a15872-8472-7163-93c9-935b36159a26\generated-triple-race'
$baselinePath='C:\Users\FC\.codex\visualizations\2026\10\20\01a15872-8472-7163-93c9-935b36159a26\baseline-v0.4\baseline-failures-v0.4.txt'
$baseline=@(Get-Content -LiteralPath $baselinePath)
foreach ($runNumber in 1..3) {
  $logPath=Join-Path $evidenceDir "run-$runNumber.txt"
  $names=@(Get-Content -LiteralPath $logPath | ForEach-Object { if ($_ -match '^\s*--- FAIL: (\S+)') { $matches[1] } } | Sort-Object -Unique)
  $leaves=@($names | Where-Object { $parent=$_; -not ($names | Where-Object { $_.StartsWith($parent+'/') }) })
  $new=@($leaves | Where-Object { $_ -notin $baseline })
  $absent=@($baseline | Where-Object { $_ -notin $leaves })
  $markers=@(Select-String -LiteralPath $logPath -Pattern 'DATA RACE|panic:|fatal error:|test timed out|\[build failed\]|\[setup failed\]|^go: ')
  "Run $runNumber : baseline=$($baseline.Count) failing_leaves=$($leaves.Count) new=$($new.Count) baseline_absent=$($absent.Count) race_panic_timeout_or_build_markers=$($markers.Count)"
}
$before=(Get-Content (Join-Path $evidenceDir 'baseline-sha256-before.txt') -Raw).Trim()
$after=(Get-Content (Join-Path $evidenceDir 'baseline-sha256-after.txt') -Raw).Trim()
$current=(Get-FileHash -LiteralPath $baselinePath -Algorithm SHA256).Hash
"Baseline SHA-256 before: $before"
"Baseline SHA-256 after:  $after"
"Baseline unchanged: $($before -eq $after -and $before -eq $current)"
if ($before -ne $after -or $before -ne $current) { throw 'Baseline checksum mismatch.' }
~~~

</details>

Real comparison output:

~~~text
Run 1 : baseline=14 failing_leaves=14 new=0 baseline_absent=0 race_panic_timeout_or_build_markers=0
Run 2 : baseline=14 failing_leaves=14 new=0 baseline_absent=0 race_panic_timeout_or_build_markers=0
Run 3 : baseline=14 failing_leaves=14 new=0 baseline_absent=0 race_panic_timeout_or_build_markers=0
Baseline SHA-256 before: 90546130FC40C45EC1E13E9CB1B3AC4AD39D47276E81CB6C3EDFED51F419E085
Baseline SHA-256 after:  90546130FC40C45EC1E13E9CB1B3AC4AD39D47276E81CB6C3EDFED51F419E085
Baseline unchanged: True
~~~

The baseline and the full raw logs remain outside Git under the task's `baseline-v0.4` and `generated-triple-race` evidence directories. **All three full runs match exactly the 14 known Windows baseline failures from issue #2, with no new failures, data races, panics, or test timeouts. This is a baseline match, not a clean full-suite pass.**

### Release metadata validation and formatting

Exact command:

~~~powershell
claude plugin validate . --strict
~~~

Exit `0`; real output:

~~~text
Validating marketplace manifest: C:\Users\FC\Downloads\Claude-Plugin\gitsize-guard\.claude-plugin\marketplace.json

√ Validation passed
~~~

Exact command:

~~~powershell
gofmt -l .
~~~

Exit `0`, with empty stdout/stderr: no unformatted Go files were listed.

The preceding three-run verification also ran `go vet ./...` with exit `0` and no stdout/stderr. Both plugin manifests now declare version `0.4.0`. Plugin validation is not a live Claude Code hook test. **Live Claude Code session testing has not been performed.**
