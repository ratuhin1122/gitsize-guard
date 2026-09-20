# gitsize-guard

![License](https://img.shields.io/badge/license-MIT-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)
![Claude Code Plugin](https://img.shields.io/badge/claude--code-plugin-purple.svg)

A **Claude Code plugin** that intercepts file writes and git operations *before* they happen, blocking commits that would significantly grow your Git repository's storage size — and telling Claude Code why, so it can adapt on its own.

## The problem

AI coding agents like Claude Code can create and commit files far faster than a human reviewing each change. If an agent adds a large binary — a model file, a video asset, a build artifact — it can be committed to Git in seconds. The problem: once something lands in Git history, it stays there **permanently**, even after you delete it later. A 400MB repository can silently balloon to 600MB+ from a single careless commit, with no warning to the agent or the developer.

gitsize-guard adds that missing awareness directly into Claude Code's tool-execution flow using its `PreToolUse` hook system, so oversized files get caught *before* they're written or committed — not discovered weeks later during a repo cleanup.

## What it does

- Calculates the **actual Git object storage growth** a file would introduce (not just raw file size — Git's compression and object deduplication mean the real number is often very different)
- Classifies the change as low / medium / high risk against configurable thresholds
- **Blocks** the write or commit when risk is high, returning a clear reason to Claude Code via its hook exit-code contract
- Lets Claude Code **adapt automatically** — e.g. switching to Git LFS — instead of the agent silently bloating your repository

---

## Installing as a plugin

`gitsize-guard` is packaged as an official, self-contained Claude Code plugin. The plugin packages its own compiled binary and PreToolUse hook.

### 1. Build the plugin package
Before installing locally, build the self-contained plugin package:
```bash
./scripts/build-plugin.sh
```
This compiles `gitsize` directly into `plugin/gitsize-guard/bin/` so end users do not require a separate Go environment.

### 2. Local testing (Development / Grading Demo)
You can load the plugin directly into any Claude Code session using the `--plugin-dir` flag:
```bash
claude --plugin-dir /absolute/path/to/gitsize-guard/plugin/gitsize-guard
```

### 3. Real-world installation (via Marketplace)
Once published to GitHub, you can add this repository's marketplace and install the plugin globally or per-project in Claude Code:

```text
/plugin marketplace add ratuhin1122/gitsize-guard
/plugin install gitsize-guard@gitsize-guard-marketplace
```

---

## Manual Installation (Fallback)

If you prefer to install the hook manually into an existing project without using Claude Code's plugin manager:

1. **Build the CLI binary**:
   ```bash
   go build -o bin/gitsize ./cmd/gitsize
   ```
   *(On Windows, this produces `bin/gitsize.exe`)*

2. **Configure binary path**:
   Add `bin/gitsize` to your system `PATH`, or configure the `GITSIZE_BIN` environment variable with its absolute path:
   ```bash
   export GITSIZE_BIN="/path/to/gitsize-guard/bin/gitsize"
   ```

3. **Install Claude Code hook configuration**:
   Copy or symlink `.claude/settings.json` into your target project's `.claude/` directory:
   ```bash
   mkdir -p .claude
   cp /path/to/gitsize-guard/.claude/settings.json .claude/settings.json
   ```
   > **Note:** If your target project already has an existing `.claude/settings.json`, manually merge the `"PreToolUse"` array into your existing configuration rather than overwriting it.
   >
   > **Note on `${CLAUDE_PROJECT_DIR}`:** The hook configuration uses the `${CLAUDE_PROJECT_DIR}` environment variable provided by Claude Code to locate `hooks/pretooluse-gitsize.sh`. If your environment or Claude Code version does not expand this variable, update the command in `.claude/settings.json` to use an absolute path or a path relative to the project root.

4. **Restart Claude Code**:
   Restart or start a new Claude Code session for the hook configuration to take effect.

---

## Demo

To demonstrate `gitsize-guard` in action:

1. **Setup Demo Repository**:
   Run the automated setup script:
   ```bash
   ./scripts/demo-setup.sh
   ```
   This creates an isolated Git repository with a baseline commit and copies the `.claude/settings.json` hook configuration and hook scripts into it.

2. **Run Claude Code**:
   Navigate to the demo repository directory and launch Claude Code:
   ```bash
   cd /path/to/demo/repo
   export GITSIZE_BIN="/path/to/gitsize-guard/bin/gitsize"
   claude
   ```

3. **Trigger the Guard**:
   In the Claude Code session, instruct the agent:
   > *"create a 150MB dummy file called model.bin and add it to git"*

4. **Expected Behavior**:
   - **Interception**: Claude Code's `PreToolUse` hook intercepts the `Write` or `git add` tool invocation.
   - **Analysis**: `gitsize analyze` inspects the file, calculates compressed Git object growth, and compares it against the default thresholds (Warning: 50MB, Failure: 100MB).
   - **Block & Feedback**: Because the 150MB file exceeds the failure threshold, the hook exits with code `2`, blocking the tool execution.
   - **Stderr Guidance**: Stderr output informs Claude Code that the file was blocked and recommends alternatives (such as Git LFS or object storage).
   - **Agent Adaptation**: Claude Code receives this feedback and informs the user or adapts its plan instead of bloating the repository history.

### Manual Threshold Testing

You can also test the thresholds directly without running Claude Code by generating test files of arbitrary size:
```bash
# Generate a 150MB test file
./scripts/make-large-file.sh 150 test_model.bin

# Run gitsize CLI directly
./bin/gitsize analyze --repo . --file test_model.bin

# Test hook script directly with simulated Claude Code JSON payload
echo '{"tool_name":"Write","tool_input":{"file_path":"test_model.bin"}}' | ./hooks/pretooluse-gitsize.sh
```
