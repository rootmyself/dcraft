#!/usr/bin/env bash
# Regenerate the TUI golden snapshots under internal/ui/tui/testdata/golden.
#
# Usage:
#   scripts/tui-golden.sh            # rewrite every snapshot and report the diff
#   scripts/tui-golden.sh -check     # fail if any snapshot is stale (no writes)
#
# The matrix is committed as text, so it is reviewable in a pull request. This
# script exists so the command lives in the repository instead of in a ticket:
# the same command the snapshots name in their own header.
#
# Snapshots live in:
#   internal/ui/tui/testdata/golden/color/<screen>.txt     painted theme
#   internal/ui/tui/testdata/golden/nocolor/<screen>.txt   NO_COLOR theme
#
# Both sets contain the visible text (escape sequences stripped), so a diff is a
# layout change, not an ANSI byte change. TestGoldenStructureDoesNotDependOnPaint
# proves the two sets differ only in paint.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
GO=${GO:-go}
PKG=./internal/ui/tui

check_only=0
case "${1:-}" in
  -check | --check) check_only=1 ;;
  "") ;;
  *)
    echo "tui-golden: unknown argument: $1 (use -check to verify without writing)" >&2
    exit 2
    ;;
esac

if [ "$check_only" = 1 ]; then
  ( cd "$ROOT" && "$GO" test "$PKG" -run 'TestGolden' -count=1 )
  echo "tui-golden: snapshots are current"
  exit 0
fi

( cd "$ROOT" && "$GO" test "$PKG" -run 'TestGolden' -count=1 -update )
echo "tui-golden: rewritten under internal/ui/tui/testdata/golden"
