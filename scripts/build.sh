#!/usr/bin/env bash
# Build reproducible dcraft distribution artifacts.
#
# Usage:
#   scripts/build.sh                      # all four supported targets, VERSION=dev
#   VERSION=v0.1.0 scripts/build.sh       # release identification without editing source
#   TARGETS="linux/arm64" scripts/build.sh
#
# Environment:
#   VERSION    version string written with -ldflags (default: dev)
#   COMMIT     source commit (default: git HEAD, else unknown)
#   BUILD_DATE build date (default: SOURCE_DATE_EPOCH or the commit date)
#   DIST_DIR   output folder, must be empty/absent (default: <repo>/dist)
#   TARGETS    space-separated subset of darwin/amd64 darwin/arm64 linux/amd64 linux/arm64
#   GO         Go toolchain command (default: go)
#
# Every artifact is built with -trimpath, -buildvcs=false, CGO_ENABLED=0 and
# deterministic -ldflags, so rebuilding the same commit with the same Go
# toolchain yields the same bytes and identification. The Go toolchain version
# is recorded in build-info.json because it is an input to reproducibility.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

VERSION=${VERSION:-dev}
COMMIT=${COMMIT:-}
BUILD_DATE=${BUILD_DATE:-}
DIST_DIR=${DIST_DIR:-$ROOT/dist}
TARGETS=${TARGETS:-"darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"}
ALLOWED="darwin/amd64 darwin/arm64 linux/amd64 linux/arm64"

GO=${GO:-go}
if ! command -v "$GO" >/dev/null 2>&1; then
  echo "build: Go toolchain not found: $GO" >&2
  exit 1
fi

case "$VERSION" in
  "" | *[!A-Za-z0-9._+-]*)
    echo "build: VERSION may only contain letters, digits, '.', '_', '+' and '-': $VERSION" >&2
    exit 1
    ;;
esac

in_git=0
if command -v git >/dev/null 2>&1 && git -C "$ROOT" rev-parse --git-dir >/dev/null 2>&1; then
  in_git=1
fi
if [ "$in_git" = 1 ] && [ -n "$(git -C "$ROOT" status --porcelain)" ]; then
  echo "build: warning: working tree has uncommitted changes; COMMIT identifies HEAD, not the modified sources" >&2
fi

if [ -z "$COMMIT" ]; then
  if [ "$in_git" = 1 ]; then
    COMMIT=$(git -C "$ROOT" rev-parse HEAD)
  else
    COMMIT=unknown
  fi
fi
case "$COMMIT" in
  "" | *[!A-Za-z0-9._+-]*)
    echo "build: COMMIT may only contain letters, digits, '.', '_', '+' and '-': $COMMIT" >&2
    exit 1
    ;;
esac

if [ -z "$BUILD_DATE" ]; then
  if [ -n "${SOURCE_DATE_EPOCH:-}" ]; then
    case "$SOURCE_DATE_EPOCH" in
      *[!0-9]*)
        echo "build: SOURCE_DATE_EPOCH must be an integer: $SOURCE_DATE_EPOCH" >&2
        exit 1
        ;;
    esac
    # macOS and BSD date use -r <seconds>; GNU date uses -d @<seconds>.
    if date -u -r "$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ >/dev/null 2>&1; then
      BUILD_DATE=$(date -u -r "$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)
    else
      BUILD_DATE=$(date -u -d "@$SOURCE_DATE_EPOCH" +%Y-%m-%dT%H:%M:%SZ)
    fi
  elif [ "$in_git" = 1 ]; then
    BUILD_DATE=$(git -C "$ROOT" log -1 --format=%cI)
  else
    BUILD_DATE=unknown
  fi
fi
case "$BUILD_DATE" in
  "" | *[!A-Za-z0-9:+._-]*)
    echo "build: BUILD_DATE contains unsupported characters: $BUILD_DATE" >&2
    exit 1
    ;;
esac

target_count=0
for target in $TARGETS; do
  target_count=$((target_count + 1))
  case " $ALLOWED " in
    *" $target "*) ;;
    *)
      echo "build: unsupported target: $target (allowed: $ALLOWED)" >&2
      exit 1
      ;;
  esac
done
if [ "$target_count" -eq 0 ]; then
  echo "build: TARGETS is empty (allowed: $ALLOWED)" >&2
  exit 1
fi

case "$DIST_DIR" in
  /*) ;;
  *) DIST_DIR="$PWD/$DIST_DIR" ;;
esac
if [ -d "$DIST_DIR" ] && [ -n "$(ls -A "$DIST_DIR" 2>/dev/null)" ]; then
  echo "build: DIST_DIR must be empty: $DIST_DIR" >&2
  exit 1
fi
mkdir -p "$DIST_DIR"

GO_VERSION=$("$GO" env GOVERSION)
case "$GO_VERSION" in
  "" | *[!A-Za-z0-9._+-]*)
    echo "build: cannot record Go toolchain version: $GO_VERSION" >&2
    exit 1
    ;;
esac

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" | awk '{print $NF}'
  else
    echo "build: need sha256sum, shasum or openssl to write checksums" >&2
    return 1
  fi
}

LD_FLAGS="-s -w"
LD_FLAGS="$LD_FLAGS -X github.com/rootmyself/dcraft/internal/buildinfo.Version=$VERSION"
LD_FLAGS="$LD_FLAGS -X github.com/rootmyself/dcraft/internal/buildinfo.Commit=$COMMIT"
LD_FLAGS="$LD_FLAGS -X github.com/rootmyself/dcraft/internal/buildinfo.BuildDate=$BUILD_DATE"

records=$(mktemp "${TMPDIR:-/tmp}/dcraft-build-records.XXXXXX")
trap 'rm -f "$records"' EXIT
trap 'exit 1' HUP INT TERM

: >"$DIST_DIR/SHA256SUMS"
: >"$records"
count=0
for target in $ALLOWED; do
  case " $TARGETS " in
    *" $target "*) ;;
    *) continue ;;
  esac
  os=${target%/*}
  arch=${target#*/}
  name="dcraft_${VERSION}_${os}_${arch}"
  echo "build: $name"
  (
    cd "$ROOT"
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$GO" build \
      -trimpath -buildvcs=false -ldflags "$LD_FLAGS" \
      -o "$DIST_DIR/$name" ./cmd/dcraft
  )
  sum=$(sha256_of "$DIST_DIR/$name")
  printf '%s  %s\n' "$sum" "$name" >>"$DIST_DIR/SHA256SUMS"
  printf '%s %s %s %s\n' "$name" "$os" "$arch" "$sum" >>"$records"
  count=$((count + 1))
done

{
  printf '{\n'
  printf '  "schema_version": 1,\n'
  printf '  "version": "%s",\n' "$VERSION"
  printf '  "commit": "%s",\n' "$COMMIT"
  printf '  "build_date": "%s",\n' "$BUILD_DATE"
  printf '  "go_version": "%s",\n' "$GO_VERSION"
  printf '  "artifacts": [\n'
  first=1
  while IFS=' ' read -r name os arch sum; do
    if [ "$first" = 1 ]; then
      first=0
    else
      printf ',\n'
    fi
    printf '    {"name": "%s", "goos": "%s", "goarch": "%s", "sha256": "%s"}' \
      "$name" "$os" "$arch" "$sum"
  done <"$records"
  if [ "$first" = 0 ]; then
    printf '\n'
  fi
  printf '  ]\n'
  printf '}\n'
} >"$DIST_DIR/build-info.json"

echo "build: wrote $count artifact(s) to $DIST_DIR"
echo "build: version=$VERSION commit=$COMMIT build_date=$BUILD_DATE go=$GO_VERSION"
echo "build: verify with 'shasum -a 256 -c SHA256SUMS' (macOS) or 'sha256sum -c SHA256SUMS' (Linux)"
