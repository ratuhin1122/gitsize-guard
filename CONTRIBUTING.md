# Contributing

1. Fork the repository and create a branch for your change.
2. Keep changes focused and describe the problem and resulting behavior.
3. Format Go files with `gofmt -w` on the files you changed.
4. Run `go vet ./...` and `go test ./...`. For concurrency-sensitive changes, also run `go test -race ./...`.
5. Open a pull request with a summary and the real verification results.

Go 1.22+ and Git are required for development. Windows hook tests require Git Bash. Known Windows test-suite gaps are tracked in [issue #2](https://github.com/ratuhin1122/gitsize-guard/issues/2); report failures honestly, including whether they reproduce before your change.

`go test ./...` includes an end-to-end test of the hook script. `go test -short ./...` skips the test that builds the binary through the script; it is a quicker development check, not a substitute for the full suite.

For behavior and scope, read the [README](README.md). Do not claim live Claude Code verification without actually performing it.
