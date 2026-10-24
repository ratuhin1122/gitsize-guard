# Changelog

## v0.4.0

- Add advisory generated-file awareness for selected working-tree files, staged blobs, and in-memory Write payloads, using path rules and generated-code markers in the first 2048 bytes.
- Distinguish strong evidence (ask at low size risk) from weak evidence (notes by default), suppress matched folders already tracked in HEAD, and aggregate findings by category with counts and total file size.
- Treat ignored files being force-added as strong evidence. Include already-staged paths selected by `git add` as advisory-only entries without adding to measured growth.
- Add generated-file flags and environment variables, strict-weak mode, and glob allowlists with flag-over-environment-over-file precedence. Read optional `.gitsizeallow` files with a 64 KiB cap and surface invalid settings in hook decisions.
- Add per-file generated metadata and report-level counts while preserving growth and existing medium/high size risk. Generated-file advice never causes a denial on its own.
- Keep production resource defaults at 5 seconds and 5000 paths; inject generous test budgets and retain dedicated tiny-budget skip coverage. Record the unchanged empty-output/exit-zero fail-open path when advisory work expires.
- Add integration coverage for staged dependency trees, HEAD suppression, ignored force-adds, Write headers, allowlists, combined near-duplicate advice, and SHA-256 checksums of every `.git` file.
- Record three consecutive uncached full race runs matching the 14 known Windows baseline failures, with no new failures, races, panics, or test timeouts. Live Claude Code session testing has not been performed.

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
