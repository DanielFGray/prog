#!/bin/bash
# A foreman worktree shares none of the main checkout's installed dependencies,
# so a dispatch into one needs them installed before the agent starts. Every
# step below is safe to re-run on an already-prepared worktree: each download
# is guarded by a test for the artifact it produces.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# System Go satisfies the go.mod 'go 1.25' directive.
if ! command -v go >/dev/null; then
	echo "go is not on PATH; install the Go toolchain (>=1.25) first" >&2
	exit 1
fi

# Module dependencies land in the shared GOMODCACHE; go mod download is
# a no-op when everything is already present.
go mod download

# The CI lint job (ci.yaml) pins golangci-lint v2.7.2. Install the same
# version locally so lint reproduces CI; the go install is cached and only
# re-run when the pinned version is absent or wrong.
golangci_version="v2.7.2"
golangci_bin="$repo_root/.bin/golangci-lint"
if [ ! -x "$golangci_bin" ] || ! "$golangci_bin" version 2>/dev/null | grep -q "version 2.7.2"; then
	mkdir -p "$repo_root/.bin"
	GOBIN="$repo_root/.bin" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$golangci_version"
fi
