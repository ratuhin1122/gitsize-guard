# Claude Code Marketplace Notes

## Manifest Structure

In `marketplace.json`:
- The `"source"` field (`"./plugin/gitsize-guard"`) is a relative path from the directory containing `marketplace.json` to the target plugin's directory.
- This allows Claude Code's plugin manager (`/plugin marketplace add`) to locate the plugin's `plugin.json` manifest and its associated hooks and binaries.
