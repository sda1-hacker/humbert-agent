#!/usr/bin/env bash
set -euo pipefail
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checks_dir=""
if [[ ${1:-} == --checks && $# == 2 ]]; then
  checks_dir="$(cd "$2" && pwd)"
elif [[ $# != 0 ]]; then
  echo "Usage: $0 [--checks /path/to/humbert-agent-checks]" >&2
  exit 2
fi
cd "$repo_dir"
go build ./cmd/... ./internal/... ./examples/...
go vet ./...
git diff --check
(cd frontend && npm run build)
if [[ -n "$checks_dir" ]]; then
  python3 "$checks_dir/run.py" "$repo_dir"
else
  echo 'Build checks passed. Behavior regression was not run; pass --checks to include the external suite.'
fi
