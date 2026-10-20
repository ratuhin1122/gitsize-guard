# Changelog

## v0.3.0

- Add advisory near-duplicate detection for working-tree files, staged blobs, and Write payloads, using deterministic content-defined chunking and SHA-256 chunk hashes.
- Compare eligible files with up to five similarly sized blobs in HEAD, with a 512 MiB read allowance, a 5-second deadline, and notes for skipped candidates.
- Close candidate-stream pipes on cancellation and bound subprocess waiting so a blocked Git stream cannot outlive the check deadline indefinitely.
- Include per-file similarity metadata and report-level near-duplicate counts. Raise low risk to medium for matching copies while preserving measured growth and existing medium/high risk decisions.
- Add near-duplicate CLI flags and environment settings, validate their values, and surface configuration errors in hook permission decisions.
- Add deterministic comparison tests and integration coverage for modified copies, unrelated files, minimum sizes, budget exhaustion, and checksums of every file under `.git` to verify analysis leaves repository contents unchanged.

## v0.2.0

- Incorporate the hook security and correctness fixes contributed by **ZAG23** in [PR #1](https://github.com/ratuhin1122/gitsize-guard/pull/1).
- Prevent execution of repository-supplied hook binaries; use trusted absolute paths or an isolated offline build of the plugin's source.
- Keep analysis from writing repository objects, closing the blocked-file retry bypass, and disable lazy fetching during Git queries.
- Resolve Bash staging and commit selections, measure Write payload content, account for Git LFS pointers, and expose visible deny/ask decisions and threshold configuration errors.
- Make the repository installable as a Claude Code plugin and marketplace, with quoted hook paths and explicit Git Bash selection on Windows.
