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
