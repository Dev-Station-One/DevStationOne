#!/usr/bin/env bash
set -euo pipefail

if ! command -v codegraph >/dev/null 2>&1; then
  echo "ERROR: CodeGraph CLI is not installed or not on PATH." >&2
  echo "Install/configure CodeGraph before completing implementation slices." >&2
  exit 1
fi

echo "CodeGraph version:"
codegraph version || true

echo "CodeGraph status:"
codegraph status || true

# CodeGraph has multiple distributions/versions with different indexing commands.
# Discover supported commands rather than assuming one.
HELP="$(codegraph --help 2>&1 || true)"

if grep -Eq '(^|[[:space:]])index([[:space:]]|$)' <<<"$HELP"; then
  echo "Running CodeGraph index..."
  codegraph index
elif grep -Eq '(^|[[:space:]])sync([[:space:]]|$)' <<<"$HELP"; then
  echo "Running CodeGraph sync..."
  codegraph sync
elif grep -Eq '(^|[[:space:]])update([[:space:]]|$)' <<<"$HELP"; then
  echo "Running CodeGraph update..."
  codegraph update
else
  echo "ERROR: Could not identify an index/sync/update command from CodeGraph CLI." >&2
  echo "$HELP" >&2
  exit 1
fi

echo "CodeGraph maintenance complete."
