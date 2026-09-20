# gitsize-guard

A Git pre-commit hook tool that guards against accidentally committing oversized files, powered by Claude Code hooks.

## Installing the hook

To register and use `gitsize-guard` with Claude Code in your project:

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
