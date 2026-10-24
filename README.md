<div align="center">

<img src="assets/banner.svg" alt="gitsize-guard — Stop AI agents from bloating your Git repo." width="1280">

[![MIT license](https://img.shields.io/badge/license-MIT-52cbd5)](LICENSE)
[![Latest tag](https://img.shields.io/github/v/tag/ratuhin1122/gitsize-guard?label=release)](https://github.com/ratuhin1122/gitsize-guard/tags)
[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8)](https://go.dev/doc/install)
[![Claude Code plugin](https://img.shields.io/badge/Claude_Code-plugin-a78bfa)](.claude-plugin/plugin.json)
[![PRs welcome](https://img.shields.io/badge/PRs-welcome-52cbd5)](CONTRIBUTING.md)
[![Open issues](https://img.shields.io/github/issues/ratuhin1122/gitsize-guard)](https://github.com/ratuhin1122/gitsize-guard/issues)

A Claude Code plugin that checks repository growth before `Write`, `git add`, and `git commit` run.

[Install](#install) · [How it works](#how-it-works) · [Configuration](#configuration) · [Limitations](#limitations) · [Contributing](#contributing)

</div>

## The problem

AI agents create and commit files faster than a person can review each change.
A model file, video, or generated artifact can enter Git history in seconds.
Deleting it later leaves the original blob in history unless that history is rewritten. GitHub also blocks individual files larger than 100 MiB.
gitsize-guard adds a size check to Claude Code's tool flow before those additions are staged or committed.

## Demo

<!-- TODO: Replace assets/demo.gif with a recording after testing in a live Claude Code session. -->
![Demo placeholder: live recording pending](assets/demo.gif)

**This is a placeholder, not a recorded demo.** The following is an illustrative denial, not captured output:

```text
Denied: adding model.bin would exceed the repository-growth threshold.
Use Git LFS for large binary files, for example: git lfs track "*.bin"
```

<details>
<summary>Try the demo or check a threshold locally</summary>

Run [scripts/demo-setup.sh](scripts/demo-setup.sh). It creates an isolated repository with a baseline commit and settings that register this checkout's hook. Open Claude Code in the directory it prints, then ask it to create a 150 MB dummy file and commit it. The expected result is a denial; a compound creation-and-commit command must first be split into separate commands. **This workflow has not been verified in a live Claude Code session.**

For a CLI check, build the binary as shown below, then use a scratch repository:

```bash
GUARD="$PWD"
cd "$(mktemp -d)" && git init -q
"$GUARD/scripts/make-large-file.sh" 150 test_model.bin
"$GUARD/bin/gitsize" analyze --file test_model.bin
```

You can also send a simulated PreToolUse JSON payload to [hooks/pretooluse-gitsize.sh](hooks/pretooluse-gitsize.sh), with `tool_name: "Bash"`, `tool_input.command: "git add test_model.bin"`, and `cwd` set to the scratch repository's absolute path.

</details>

## Features

- Blocks oversized `Write` payloads and `git add` / `git commit` operations.
- Detects exact content reuse against `HEAD` so eligible duplicate blobs add zero measured growth.
- Warns about near-duplicates of another HEAD path (**v0.3.0**, advisory only).
- Flags likely generated files and dependency trees (**v0.4.0**, advisory only), with confidence tiers and allowlists.
- Measures Git LFS pointers without executing the LFS filter.
- Never writes objects or refreshes the index in your repository's `.git` directory.
- Uses deterministic measurements and chunking; no AI participates in measuring content. Time budgets can affect whether an advisory completes.

<details>
<summary>Supported command forms</summary>

Static parsing handles file paths, directories, globs, `git add .`, `-A`, `-u`, `--renormalize`, `git commit`, `-a`, and commit paths. It also recognizes `git -C`, `--work-tree` with its own `.git`, Git aliases, `cd dir && ...`, and `cd "$(git rev-parse --show-toplevel)"`.

Inline shell forms include `sh -c`, `bash` heredocs, and Claude Code's `git commit -m "$(cat <<'EOF' ... EOF)"` pattern. Ambiguous selections are estimated and surfaced through an `ask`; parsing is best-effort.

</details>

## Install

Add the marketplace, then install the plugin:

```text
/plugin marketplace add ratuhin1122/gitsize-guard
/plugin install gitsize-guard@gitsize-guard-marketplace
```

**Requirements:** Git and Go **1.22+**, or an absolute `GITSIZE_BIN` path to a binary you built yourself. **Windows requires Git Bash.** Git **2.44+** is needed to prevent lazy fetching in partial clones; older versions ignore `GIT_NO_LAZY_FETCH`, so treeless clones may fetch trees.

The first Git command builds the binary offline (`GOPROXY=off`, `GOTOOLCHAIN=local`) into `${CLAUDE_PLUGIN_DATA}`. Source changes trigger a rebuild. This repository contains both the [plugin](.claude-plugin/plugin.json) and its [marketplace](.claude-plugin/marketplace.json), with [hook registration](hooks/hooks.json).

<details>
<summary>Local checkout and manual installation</summary>

To load a checkout through the plugin manager:

```bash
claude --plugin-dir /absolute/path/to/gitsize-guard
```

Alternatively, register the script **from your trusted gitsize-guard checkout** in `~/.claude/settings.json` or a project's `.claude/settings.json`. Do not copy the script into other repositories.

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash|Write",
      "hooks": [{
        "type": "command",
        "command": "\"/absolute/path/to/gitsize-guard/hooks/pretooluse-gitsize.sh\"",
        "shell": "bash",
        "timeout": 30
      }]
    }]
  }
}
```

Merge this entry into an existing `PreToolUse` array; do not overwrite other hooks. Keep the escaped quotes around paths containing spaces, or the hook may silently fail. `"shell": "bash"` selects Git Bash on Windows instead of resolving a WSL `bash.exe` from `PATH`.

The checkout's own [.claude/settings.json](.claude/settings.json) uses `$CLAUDE_PROJECT_DIR`. Start a new Claude Code session after changing hook settings.

</details>

## How it works

```text
Claude Code Bash / Write call
             |
        PreToolUse hook
             |
          Go CLI ---> Git: paths, sizes, HEAD blobs
             |
      measure + assess risk
             |
  deny / ask / normal permission flow
```

Growth means **uncompressed new blob bytes**, counted per repository; it is not a prediction of packed disk usage. Git LFS content counts as its pointer. Other filters and working-tree encodings are measured in a temporary object directory outside the repository.

| Situation | Decision |
| --- | --- |
| Exact growth reaches 100 MB (default) | `deny` |
| Exact growth reaches 50 MB (default) | `ask` |
| Estimated growth reaches a threshold | `ask` |
| Near-duplicate found while size risk is low | `ask`; growth stays unchanged |
| Strong generated-file match (or strict weak match) while size risk is low | `ask`; growth stays unchanged |
| Files/index change before staging or committing in the same command | `deny`; run the Git step separately |
| Unresolved repository or Git analysis error | Usually `ask`; see failure modes below |
| No actionable risk | No output; normal Claude Code permissions apply |

The hook **never emits `allow`**. Reasons accompany `deny` and `ask` decisions, including near-duplicate and generated-file advice. Gitignored `Write` targets are skipped.

<details>
<summary>Measurement details, CLI, and security model</summary>

Plain files under 1 MiB count at face value without hashing. Larger files are hashed to detect reuse in `HEAD`; after 1 GiB of hashing per check, remaining files count at face value. Non-LFS filters run for up to 64 files of at least 1 MiB per check; remaining filtered files count at working-tree size. Your filter commands can still have their own side effects.

A command such as `dd ... && git add model.bin && git commit`, or `git stash pop && git commit`, changes files or the index before the guard can measure them. It is denied with instructions to run the Git step separately. Branch creation (`git checkout -b`), tests, and checksums do not count as those mutations.

Build and inspect JSON reports directly:

```bash
go build -o bin/gitsize ./cmd/gitsize
./bin/gitsize analyze --file model.bin --file assets/video.mp4
./bin/gitsize analyze --staged
./bin/gitsize analyze --pending --failure-mb 200
```

File paths are relative to the current directory. `--staged` checks what a commit would record; `--pending` checks pending additions. `analyze` exits `0` for low/medium risk, `1` for high risk, and `2` for errors. `gitsize hook` is the PreToolUse entry point; its protocol uses exit `0` with a decision or empty output.

The shim runs binaries only from an absolute `GITSIZE_BIN`, its trusted source build, or an absolute `PATH` entry. Relative `PATH` entries are removed before resolving tools. Keep your own `PATH` entries absolute.

Source builds are cached per checkout and isolated from `GOOS`, `GOARCH`, `GOFLAGS`, `go.work`, saved `go env -w` settings, and VCS stamping. Every Git call sets `GIT_NO_LAZY_FETCH=1` and `GIT_OPTIONAL_LOCKS=0`; the analyzer avoids object writes and index refreshes in the repository.

</details>

## Configuration

Defaults: **50 MB warning**, **100 MB block**, and near-duplicate detection enabled at **90% similarity** for files of **5 MiB or more**. Size options named `MB` use 1,048,576 bytes (MiB).

Set environment variables **before launching Claude Code**. Commands run by Claude cannot change the hook's parent environment. Explicit flags override the corresponding environment variable independently; the threshold, near-duplicate, and generated-file flags work with both `analyze` and `hook`.

<details>
<summary>All configuration flags and environment variables</summary>

| CLI flag | Environment variable | Default | Meaning / accepted values |
| --- | --- | --- | --- |
| `--warning-mb` | `GITSIZE_WARNING_MB` | `50` | Growth that triggers confirmation |
| `--failure-mb` | `GITSIZE_FAILURE_MB` | `100` | Exact growth that triggers denial |
| `--near-dup=<bool>` | `GITSIZE_NEARDUP` | `true` / `1` | Flag: `true` / `false`; env: `0` / `1` |
| `--near-dup-threshold` | `GITSIZE_NEARDUP_THRESHOLD` | `0.90` | Finite number from `0.5` to `1.0` |
| `--near-dup-min-mb` | `GITSIZE_NEARDUP_MIN_MB` | `5` | Whole MiB, `1`–`1048576` |
| `--generated=<bool>` | `GITSIZE_GENERATED` | `true` / `1` | Flag: `true` / `false`; env: `0` / `1` |
| `--generated-allow <glob>` | `GITSIZE_GENERATED_ALLOW` | `.gitsizeallow`, if present | Repeat the flag; separate env globs with commas |
| `--generated-strict-weak=<bool>` | `GITSIZE_GENERATED_STRICT_WEAK` | `false` / `0` | Ask on weak evidence; env: `0` / `1` |
| — | `GITSIZE_BIN` | — | Absolute path to a trusted binary instead of building |

Setting only one size threshold moves the other to meet it if necessary: `GITSIZE_FAILURE_MB=40` alone lowers the warning to 40 MB.

Invalid settings make `analyze` return an error. The hook keeps valid settings, substitutes defaults for invalid ones, and surfaces configuration errors through an `ask` on staging or `Write` calls. An existing `deny` stays a `deny`.

```bash
gitsize analyze --pending --near-dup-threshold 0.95 --near-dup-min-mb 12
gitsize analyze --pending --near-dup=false
```

</details>

## Near-duplicate detection

The analyzer compares large working-tree files, staged blobs, and `Write` content with **another path in HEAD**. Deterministic content-defined chunks recognize shared content even after insertions. Similarity is the fraction of new-file bytes covered by matching chunks, respecting the multiplicity of old chunks. Updating the same HEAD path does not count as adding a duplicate.

This is **advisory only**: it never reduces `growth_bytes`, lowers risk, or raises risk to high. A match raises low risk to medium, causing an `ask`; medium and high risk retain their level. The reason recommends replacing the existing file rather than adding a copy, or tracking versions with Git LFS / external storage.

<details>
<summary>Candidates, budgets, and JSON output</summary>

At most five HEAD candidates are compared, with sizes from half to twice the new file's size, ordered by size proximity and then path. Git LFS pointers are skipped. Checks share a **512 MiB read budget** and a **5-second production deadline**. Budget limits and unavailable local blobs cause skips with human-readable `notes` in the CLI report, rather than analysis failure.

Matches add per-file `near_duplicate` metadata (`path`, `similarity`, `shared_bytes`, `candidate_size`) and a report-level `near_duplicates` count. Reasons include sentences such as “copy.bin is ~94% identical to original.bin already in HEAD”.

</details>

## Generated-file awareness

Enabled by default in **v0.4.0**, this advisory detects likely dependency trees, caches, build output, coverage reports, compiled/minified files, source maps, logs, OS metadata, and generated-code headers. It checks selected working-tree files, staged blobs, and `Write` content; content inspection reads only the first **2048 bytes**. It groups findings by category into a count and total file size, rather than emitting one reason per file.

| Confidence | Default behavior |
| --- | --- |
| **Strong** | Raises low risk to medium so the hook asks for confirmation. |
| **Weak** | Adds a CLI report note without changing risk. `--generated-strict-weak` requests the same confirmation as strong evidence; JSON confidence remains `weak`. |

A matching folder already represented by files in **HEAD** is suppressed, with no generated-file annotation or note. Merely staging a folder does not count as intentional HEAD content. Files ignored by Git but being added anyway (for example, with `git add -f`) provide strong evidence, subject to the same allowlist and HEAD suppression rules.

Generated-file advice **never changes `growth_bytes`, lowers risk, or raises risk above medium**. Existing size-based denials still apply. Recommendations include adding the folder to `.gitignore`, running `git rm -r --cached <folder>` if already staged, or storing build output in release assets or CI artifacts. The guard does not run those fixes for you.

<details>
<summary>Detection rules</summary>

- **Strong path segments:** `node_modules`, `__pycache__`, `.next`, `.nuxt`, `.gradle`, `.pytest_cache`, `.mypy_cache`, `coverage`, `.nyc_output`, `htmlcov`, `.terraform`.
- **Strong filenames:** `*.pyc`, `*.pyo`, `*.class`, `*.o`, `*.obj`, `*.exe`, `*.dll`, `*.so`, `*.dylib`, `*.min.js`, `*.min.css`, `*.map`, `*.log`, `.DS_Store`, `Thumbs.db`.
- **Weak directory segments:** `dist`, `build`, `out`, `target`, `vendor`, `bin`, `obj`, `.cache`. Names such as `rebuild` and `builder.go` do not match.
- **Strong content markers:** `Code generated` together with `DO NOT EDIT` on one line, `@generated`, `This file was automatically generated`, or `auto-generated by`. The last two are case-insensitive. A NUL byte in the inspected prefix disables content-marker matching.

Path rules normalize backslashes and a leading `./`. They match whole directory segments and are case-sensitive except for `.DS_Store` and `Thumbs.db`.

</details>

### Settings and allowlists

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--generated=<bool>` | `GITSIZE_GENERATED` (`0` / `1`) | Enabled |
| `--generated-allow <glob>` (repeatable) | `GITSIZE_GENERATED_ALLOW` (comma-separated globs) | Repository `.gitsizeallow`, if present |
| `--generated-strict-weak=<bool>` | `GITSIZE_GENERATED_STRICT_WEAK` (`0` / `1`) | Disabled |

Allowlist precedence is **explicit flags → nonempty environment setting → repository file**. The winning list replaces the lower-priority lists; they are not merged. Allowlisted paths produce no generated-file annotation, reason, or note. They remain subject to size and near-duplicate checks. Invalid boolean values, globs, or repository-file settings are surfaced through an `ask` in the hook; an existing `deny` remains a `deny`. `analyze` returns an error for invalid configuration.

Create an optional `.gitsizeallow` in the repository root, with one glob per line. Blank lines and `#` comments (including trailing comments) are ignored. The file is read-only and capped at **64 KiB**:

```text
# Deliberately checked-in output
dist/**
vendor/**
```

Globs are relative to the repository root and case-sensitive. Both slash styles are accepted. `*` and `?` stay within a path segment; a whole `**` segment spans zero or more directories. A trailing slash allows a directory tree, so `dist/` also covers its descendants.

```bash
gitsize analyze --staged --generated-strict-weak
gitsize analyze --pending --generated-allow 'dist/**' --generated-allow 'vendor/**'
gitsize analyze --pending --generated=false
```

<details>
<summary>Budgets and JSON output</summary>

Production checks retain a **5-second time budget** and a **5,000-path cap**. Skipped work is recorded in CLI report `notes`. Tests can inject different limits; large-tree tests use a 60-second budget without changing production defaults.

Per-file `generated` metadata contains `category`, `confidence` (`strong` or `weak`), `rule`, and `ignored_by_gitignore`. Reports include `generated_files`, a `generated_by_category` count map, and notes as needed.

</details>

### Generated-file limitations

- Path and marker heuristics can misfire; use the allowlist for intentional files.
- Only inspects files being added through the checked staging/commit selection or supplied by a `Write` call; it is not a repository-wide audit.
- **Advisory only: asks, never denies on its own.** Weak evidence normally adds only a note. Size-based denials remain independent.
- Does not remove files already committed, unstage files, or rewrite history.
- Budget exhaustion can skip an advisory. A skip note alone does not trigger a hook decision, so a low-size-risk check can return empty output with exit `0`.

## Limitations

- Only Claude Code **Bash and Write tool calls** trigger the hook. Manual Git commits and the `Edit` tool bypass it. For external commits, consider a separate pre-commit check using `gitsize analyze --staged`.
- Bash detection uses **static, best-effort parsing**. Ambiguous selections are estimated from pending files and prompt for confirmation.
- Duplicate detection compares against **HEAD only**, not other staged files or older history.
- Already-compressed ZIP, MP4, and some model formats may share few chunks despite semantic similarity. Chunk overlap does not replace Git's pack delta compression.
- The hook **can fail open**: a crash or timeout can produce empty output with exit `0`. A skipped near-duplicate or generated-file advisory can also leave no decision when measured size risk is low; completed size checks still apply.
- Windows test-suite gaps are tracked in [issue #2](https://github.com/ratuhin1122/gitsize-guard/issues/2). See [VERIFICATION.md](VERIFICATION.md) for recorded checks.
- **Not yet tested in a live Claude Code session.** The demo remains a placeholder.

<details>
<summary>Additional bypasses and failure modes</summary>

- Missing or inaccessible working directories can yield no decision because repository lookup errors are treated as selections outside a Git work tree. Earlier intermittent empty decisions were not conclusively traced to machine load.
- If no binary is available, staging commands receive an `ask` explaining that the guard is inactive. A crashing or timed-out binary instead fails open.
- Opaque scripts (`bash release.sh`), `make`, and npm scripts that call Git internally are not inspected. They are assumed to change files, but not the index; `npm run build && git commit -m x` is therefore not blocked as a compound mutation.
- `git merge`, `cherry-pick`, `am`, `revert`, and `commit-tree` can create commits without a separate checked `git commit`.
- Content placed in the index by those operations, `git apply --cached`, `git stash pop`, or `git update-index` is checked by a later `git commit`; known index mutations followed by commit in the same command are denied.
- Each `git add` is judged separately. Multiple approved additions can still produce a denied combined commit.
- Non-LFS filters execute in a temporary object directory, but their own commands can have side effects. Older Git versions can still fetch trees in partial clones (see requirements).

</details>

## Roadmap

- Verify the plugin in a live Claude Code session and record the demo.
- Address the Windows test-suite gaps in issue #2.
- Explore historical repository-growth tracking.

## Contributing

Fork the repository, create a branch, and open a pull request. See [CONTRIBUTING.md](CONTRIBUTING.md) for formatting and verification commands.

## Credits

Thanks to **ZAG23** for the v0.2.0 security and correctness work in [PR #1](https://github.com/ratuhin1122/gitsize-guard/pull/1). See [CHANGELOG.md](CHANGELOG.md) for release history.

## License

[MIT](LICENSE).
