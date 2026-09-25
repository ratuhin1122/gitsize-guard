# gitsize-guard

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)
![Claude Code Plugin](https://img.shields.io/badge/claude--code-plugin-purple.svg)

A **Claude Code plugin** that checks `git add` / `git commit` commands (and large file writes) *before* they run, blocks the ones that would significantly grow your Git repository, and tells Claude Code why so it can adapt on its own.

## The problem

AI coding agents like Claude Code can create and commit files far faster than a human reviewing each change. If an agent adds a large binary — a model file, a video asset, a build artifact — it can be committed to Git in seconds. Once something lands in Git history it stays there **permanently**, even after you delete it later, and GitHub rejects any file over 100 MB outright.

gitsize-guard adds that missing awareness directly into Claude Code's tool-execution flow using its `PreToolUse` hook, so oversized additions get caught *before* they're staged or committed — not discovered weeks later during a repo cleanup.

## What it does

- Works out **exactly which files a command would stage or commit**: `git add <files>`, `git add .`, `git add -A`, `git add -u`, directories, globs, `git -C <dir>`, `--work-tree` with its own `.git`, `cd dir && ...` (including `cd "$(git rev-parse --show-toplevel)"`), `git commit`, `git commit -a`, `git commit <paths>`, `git add --renormalize`, git aliases, scripts passed to `sh -c` or fed to `bash <<EOF`, and the `git commit -m "$(cat <<'EOF' ... EOF)"` form Claude Code itself uses.
- Measures the **bytes git would newly store**: content already in `HEAD` counts as zero, **Git LFS** files count as the small pointer git stores (without running the LFS filter), and other filtered or re-encoded files are measured in a throwaway object directory.
- **Denies** the command when the exact total reaches the failure threshold (default 100 MB), with the largest files and a recommendation (e.g. `git lfs track "*.bin"`) fed back to Claude Code.
- **Denies** a command that creates or changes files (or the index) and then stages or commits in the same line (e.g. `dd ... && git add model.bin && git commit`, or `git stash pop && git commit`), since that content can't be measured before the command runs. Claude is told to run the git step as its own command, which is then checked exactly. Commands that can't change anything, like `git checkout -b`, `go test` or checksums, don't count.
- **Asks you** to confirm at the warning threshold (default 50 MB), and when it can only estimate what a command stages (variables in paths, `xargs git add`), or can't tell which repository it targets.
- Also checks `Write` tool calls whose content alone would cross a threshold (unless the target is gitignored).
- **Doesn't modify your repository**: no objects are written, no index refresh, and no network access.

---

## Requirements

- `git` (2.44+ for partial clones: older versions ignore `GIT_NO_LAZY_FETCH`, so a treeless clone may fetch trees)
- Go 1.22+ so the plugin can build its `gitsize` binary from source on first use (offline, with `GOPROXY=off` and `GOTOOLCHAIN=local`). Without Go, point `GITSIZE_BIN` at a `gitsize` binary you built yourself.

## Installing as a plugin

This repository is both the plugin and its marketplace (`.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `hooks/hooks.json`).

### From GitHub (marketplace)

```text
/plugin marketplace add ratuhin1122/gitsize-guard
/plugin install gitsize-guard@gitsize-guard-marketplace
```

### Local checkout

```bash
claude --plugin-dir /absolute/path/to/gitsize-guard
```

The first git command after installing builds `gitsize` into the plugin's data directory (`${CLAUDE_PLUGIN_DATA}`); it is rebuilt automatically when the plugin's source changes.

---

## Manual installation (without the plugin manager)

Register the hook script **from your gitsize-guard checkout** (don't copy it into other repositories) in `~/.claude/settings.json` or a project's `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash|Write",
        "hooks": [
          {
            "type": "command",
            "command": "bash \"/absolute/path/to/gitsize-guard/hooks/pretooluse-gitsize.sh\"",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

> **Note:** If the settings file already has hooks, merge this entry into the existing `"PreToolUse"` array rather than overwriting it. Keep the quotes around the path: unquoted paths containing spaces silently disable the hook.

This repository's own `.claude/settings.json` does the same for sessions opened in the checkout, using `"$CLAUDE_PROJECT_DIR"`.

Restart or start a new Claude Code session for the hook configuration to take effect.

---

## Configuration

Set these in the environment Claude Code runs in (commands Claude runs cannot change them):

| Variable | Default | Meaning |
| --- | --- | --- |
| `GITSIZE_WARNING_MB` | `50` | Growth at which you're asked to confirm |
| `GITSIZE_FAILURE_MB` | `100` | Growth at which the command is denied |
| `GITSIZE_BIN` | — | Absolute path to a `gitsize` binary to use instead of building one |

Setting only one threshold moves the other to meet it: `GITSIZE_FAILURE_MB=40` on its own also lowers the warning to 40 MB. An invalid value is replaced by its default (valid settings are kept) and reported on the next git staging command, rather than silently ignored.

## How decisions are made

| Situation | Hook output |
| --- | --- |
| Exact growth ≥ failure threshold | `deny`: the command is blocked and the reason is shown to Claude |
| Files or the index are changed, then staged or committed, in the same command | `deny`, telling Claude to run the git step separately |
| Estimated growth (the command's file set couldn't be determined) ≥ a threshold | `ask`: you're asked to confirm, with the reason |
| Exact growth ≥ warning threshold | `ask` |
| A git staging command whose repository it can't determine, or a `git` error | `ask` (such notes are also appended to any other decision) |
| No `gitsize` binary available | `ask` on git staging commands, saying the guard is inactive |
| Anything else | no output: Claude Code's normal permission flow applies |
| `gitsize` crashes or times out | no output (fails open) |

The hook never emits `allow`, so it can't bypass your permission settings.

"Growth" is the uncompressed size of the new blobs, which is what GitHub's per-file limit applies to, counted per repository. Files under 1 MiB are counted at their size without hashing. Larger ones are hashed so that content already in `HEAD` counts as zero; past 1 GiB of hashing per check, further files are counted at their size.

## Security model

The hook only runs a `gitsize` binary from places you control: `GITSIZE_BIN` (absolute paths only), a build of the plugin's own source (cached per checkout, rebuilt when the source changes, built offline, ignoring `GOOS`/`GOARCH`/`GOFLAGS`, `go.work`, `go env -w` settings and VCS stamping), or an absolute `PATH` entry. The shim also drops relative `PATH` entries before running anything, so a cloned repository can't supply the tools it (or `gitsize`) runs. Every git call runs with `GIT_NO_LAZY_FETCH=1`.

One thing the plugin can't control: Claude Code starts the hook with `bash` looked up on your `PATH`, as it does for every command hook. If your `PATH` puts a relative entry (like `./node_modules/.bin`) ahead of the system directories, a repository could supply that `bash`, for this and every other hook. Keep `PATH` entries absolute.

## Limitations

- Command analysis is static and best-effort. When a command stages files it can't pin down, it estimates from everything pending in the repository and asks rather than skipping the check.
- Commands that commit through something opaque aren't seen: a script file (`bash release.sh`), `make`, or an `npm run` script that runs git internally. Such commands are assumed to change files but not the index, so `npm run build && git commit -m x` isn't blocked.
- `git merge`, `git cherry-pick`, `git am` and `git revert` create commits without a separate `git commit`, so they aren't checked, and neither is `git commit-tree`. Content they (or `git apply --cached`, `git stash pop`, `git update-index`) put in the index is checked by a later `git commit`, which is denied if it's in the same command line.
- Each `git add` is judged on its own, while `git commit` is judged on everything staged. Two approved warning-level adds can therefore add up to a denied commit.
- The `Edit` tool isn't checked (edits can't realistically create files this large), and neither are commits made outside Claude Code. For those, pair this with a git `pre-commit` hook such as `gitsize analyze --staged`.
- Deduplication is against `HEAD` only, not all of history.
- Filters other than Git LFS are run (into a throwaway object directory) to measure their output, for up to 64 files of 1 MiB or more per check, so whatever side effects your own filter commands have still happen. Other filtered files count at their working-tree size.

---

## CLI

```bash
go build -o bin/gitsize ./cmd/gitsize

# One or more files (paths relative to the current directory)
./bin/gitsize analyze --file model.bin --file assets/video.mp4

# What `git commit` would record, or everything `git add -A && git commit` would
./bin/gitsize analyze --staged
./bin/gitsize analyze --pending --failure-mb 200
```

`analyze` prints a JSON report and exits `0` for low/medium risk, `1` for high risk, and `2` on errors. `gitsize hook` is the PreToolUse entry point used by `hooks/pretooluse-gitsize.sh`.

## Demo

1. **Setup demo repository**:
   ```bash
   ./scripts/demo-setup.sh
   ```
   This creates an isolated Git repository with a baseline commit and a `.claude/settings.json` that registers this checkout's hook.

2. **Run Claude Code** in the directory it prints:
   ```bash
   cd /path/to/demo/repo
   claude
   ```

3. **Trigger the guard** by asking:
   > *"create a 150MB dummy file called model.bin and commit it"*

4. **Expected behavior**: the `git add` / `git commit` command is denied with the size, the offending file, and a Git LFS recommendation. If Claude creates and commits the file in one command, it's told to run the git step separately first, and that is then denied the same way. Retrying is denied again, and Claude Code adapts its plan instead of bloating the repository history.

### Manual threshold testing

Use a scratch repository so the test file doesn't end up in this checkout:

```bash
# A scratch repository with a 150MB test file
GUARD="$PWD"; cd "$(mktemp -d)" && git init -q
"$GUARD/scripts/make-large-file.sh" 150 test_model.bin

# Run the CLI directly
"$GUARD/bin/gitsize" analyze --file test_model.bin

# Run the hook with a simulated Claude Code payload
echo '{"tool_name":"Bash","tool_input":{"command":"git add test_model.bin"},"cwd":"'"$PWD"'"}' | "$GUARD/hooks/pretooluse-gitsize.sh"
```

## Development

```bash
go test ./...          # includes an end-to-end test of the hook script
go test -short ./...   # skips the test that builds the binary through the hook script
```
