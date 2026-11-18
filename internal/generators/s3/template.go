package s3

// The generated scripts are plain bash reviewed by a human maintainer. Comments
// are Korean; commands and flag names stay English. The bodies are valid for
// bash 3.2 (macOS) and modern Linux bash.

const syncScriptTemplate = `#!/usr/bin/env bash
# =============================================================================
# dcraft S3 호스트 스크립트 (dcraft generate-s3-hook로 생성)
#
# 입력
#   - S3 원본: {{SOURCE}}
#   - 방식: {{MODE_LABEL}}
#   - 받을 폴더: {{DEST}}
#   - {{PROFILE_NOTE}}
#
# 실행 위치
#   - dcraft 훅으로 등록하면 프로젝트 폴더에서 실행됩니다.
#   - 직접 실행해도 이 스크립트는 자기 위치에서 프로젝트 폴더를 계산합니다.
#     프로젝트 폴더: {{PROJECT_DIR}}
#     출력 스크립트가 프로젝트 안에 있으면 프로젝트를 옮겨도 상대 위치가 같을 때
#     그대로 동작합니다.
#
# 하는 일
#   1) aws CLI가 PATH에 있는지 확인합니다. 없으면 설치 안내를 출력하고 종료합니다.
#   2) 받을 폴더가 프로젝트 안에 있고 심볼릭 링크가 아닌지 확인합니다.
#   3) 기존 AWS CLI 구성의 자격 증명으로 다음 명령을 실행합니다.
#        aws s3 sync <원본> <받을 폴더> --only-show-errors --no-progress
#      (AWS 프로필을 지정했다면 --profile <이름>이 추가됩니다.)
#   4) 원본에 없는 받을 폴더의 기존 파일을 삭제하는 삭제 동기화는 하지 않습니다.
#      파일을 내려받는 것 외에 원격 S3를 변경하지 않습니다.
#
# 변경되는 파일
#   - 받을 폴더의 파일만 추가하거나 같은 이름으로 갱신합니다.
#   - 원본에 없는 받을 폴더의 기존 파일은 삭제하지 않습니다.
#   - S3 원격과 이 스크립트 파일은 실행 중 변경하지 않습니다.
#
# 전제 조건
#   - 실행 시 aws CLI가 PATH에 있어야 합니다. 이 스크립트는 도구를 자동 설치하지 않습니다.
#   - 기존 AWS CLI 구성(환경 변수, ~/.aws/config·credentials, SSO, 역할 등)을 그대로 사용합니다.
#     aws configure를 실행하거나 자격 증명을 저장·출력하지 않습니다.
#   - S3 원본에는 사용자 이름/비밀번호/토큰/미리 서명된 쿼리를 넣을 수 없습니다.
#   - AWS CLI가 출력하는 오류 메시지는 사용자의 AWS CLI 출력이며 dcraft가 제어하지 않습니다.
#
# 실패 시
#   - 메시지를 출력하고 0이 아닌 코드로 종료합니다. 사전(pre) 훅이면 dcraft가
#     본 작업(up/update)을 시작하지 않습니다.
#   - 동기화 도중 실패하면 받을 폴더에 일부 파일이 남아 있을 수 있습니다.
#     dcraft는 자동으로 되돌리지 않습니다.
#
# 수정 방법
#   - SRC, DEST, PROFILE 값을 직접 고치거나 dcraft generate-s3-hook로 다시 생성하세요.
#   - 이 파일의 내용이 바뀌면 dcraft의 스크립트 신뢰(script_trust) 승인을 다시 받아야 합니다.
#
# 다음 단계 (자동 등록·자동 실행 아님)
#   dcraft hooks --add --stage pre --script {{SCRIPT_ARG}}
#   dcraft up --approve script_trust   (또는 대화형 화면에서 신뢰 승인)
# =============================================================================
set -euo pipefail

SRC={{SOURCE_SH}}
PROFILE={{PROFILE_SH}}
SELF_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
{{PROJECT_DIR_EXPR}}
{{DEST_EXPR}}

say() { printf '%s\n' "$*"; }
die() { printf '오류: %s\n' "$*" >&2; exit 1; }

command -v aws >/dev/null 2>&1 || die "aws CLI를 찾지 못했습니다. AWS CLI를 설치한 뒤 PATH를 확인하세요. (이 스크립트는 도구를 자동 설치하지 않습니다.)"

check_source() {
  [ -n "$SRC" ] || die "S3 원본 값이 비어 있습니다. 이 파일의 SRC를 확인하세요."
  case "$SRC" in
    s3://*) ;;
    *) die "S3 원본은 s3://bucket/... 형식이어야 합니다. 이 파일의 SRC를 확인하세요." ;;
  esac
  case "$SRC" in
    *"?"*|*"#"*) die "S3 원본에 쿼리 문자열이나 미리 서명된 URL을 사용할 수 없습니다. 기존 AWS CLI 자격 증명을 사용하세요." ;;
  esac
  authority=${SRC#s3://}
  authority=${authority%%/*}
  case "$authority" in
    ''|*@*) die "S3 원본의 버킷/인증 정보 형식이 올바르지 않습니다. 기존 AWS CLI 자격 증명을 사용하세요." ;;
  esac
  case "$SRC" in
    *.tar.gz) die "이 스크립트는 sync 방식입니다. .tar.gz 객체는 tar 방식으로 생성하세요." ;;
  esac
}

prepare_destination() {
  local rel part cur
  [ -d "$PROJECT_DIR" ] || die "프로젝트 폴더를 찾지 못했습니다: $PROJECT_DIR"
  if [ -L "$DEST" ]; then
    die "받을 폴더가 심볼릭 링크입니다. 심볼릭 링크 너머로 쓰지 않습니다: $DEST"
  fi
  if [ -e "$DEST" ] && [ ! -d "$DEST" ]; then
    die "받을 폴더 경로에 폴더가 아닌 파일이 있습니다. 덮어쓰지 않습니다: $DEST"
  fi
  rel=$DEST
  case "$rel" in
    "$PROJECT_DIR"/*) rel=${rel#"$PROJECT_DIR"/} ;;
    *) rel='' ;;
  esac
  cur=$PROJECT_DIR
  while [ -n "$rel" ]; do
    case "$rel" in
      */*) part=${rel%%/*}; rel=${rel#*/} ;;
      *) part=$rel; rel='' ;;
    esac
    case "$part" in
      ''|'.') continue ;;
    esac
    cur="$cur/$part"
    if [ -L "$cur" ]; then
      die "받을 폴더 경로에 심볼릭 링크가 있습니다. 심볼릭 링크 너머로 쓰지 않습니다: $cur"
    fi
  done
  mkdir -p -- "$DEST" || die "받을 폴더를 만들지 못했습니다: $DEST"
  DEST_REAL=$(CDPATH= cd -- "$DEST" && pwd -P) || die "받을 폴더의 실제 경로를 확인하지 못했습니다: $DEST"
  [ "$DEST_REAL" != "$PROJECT_DIR" ] || die "받을 폴더가 프로젝트 폴더 자체입니다. 프로젝트 안의 별도 폴더를 사용하세요."
  case "$DEST_REAL" in
    "$PROJECT_DIR"|"$PROJECT_DIR"/*) ;;
    *) die "받을 폴더가 프로젝트 폴더 밖을 가리킵니다. 심볼릭 링크를 확인하세요: $DEST" ;;
  esac
  case "$SELF_DIR" in
    "$DEST_REAL"|"$DEST_REAL"/*) die "받을 폴더가 스크립트 폴더를 포함합니다. 배포 파일이 스크립트를 덮을 수 있어 중단합니다." ;;
  esac
  if [ -n "$(find "$DEST" -type l -print -quit 2>/dev/null)" ]; then
    die "받을 폴더 안에 심볼릭 링크가 있습니다. 심볼릭 링크 너머로 쓰지 않습니다: $DEST"
  fi
}

check_source
prepare_destination

say "S3 원본: $SRC"
say "받을 폴더: $DEST"
say "원본에 없는 기존 파일을 삭제하는 삭제 동기화는 사용하지 않습니다."
if [ -n "$PROFILE" ]; then
  say "AWS CLI 프로필: $PROFILE (기존 AWS CLI 구성)"
  if ! aws s3 sync "$SRC" "$DEST" --profile "$PROFILE" --only-show-errors --no-progress; then
    die "동기화에 실패했습니다. 받을 폴더에 일부 파일이 남아 있을 수 있습니다. AWS CLI 오류 메시지는 사용자의 AWS CLI가 출력한 것입니다."
  fi
else
  if ! aws s3 sync "$SRC" "$DEST" --only-show-errors --no-progress; then
    die "동기화에 실패했습니다. 받을 폴더에 일부 파일이 남아 있을 수 있습니다. AWS CLI 오류 메시지는 사용자의 AWS CLI가 출력한 것입니다."
  fi
fi
say "완료: $DEST"
say "받을 폴더에만 있는 기존 파일은 삭제하지 않았습니다."
`

const tarScriptTemplate = `#!/usr/bin/env bash
# =============================================================================
# dcraft S3 호스트 스크립트 (dcraft generate-s3-hook로 생성)
#
# 입력
#   - S3 원본: {{SOURCE}}
#   - 방식: {{MODE_LABEL}}
#   - 받을 폴더: {{DEST}}
#   - {{PROFILE_NOTE}}
#
# 실행 위치
#   - dcraft 훅으로 등록하면 프로젝트 폴더에서 실행됩니다.
#   - 직접 실행해도 이 스크립트는 자기 위치에서 프로젝트 폴더를 계산합니다.
#     프로젝트 폴더: {{PROJECT_DIR}}
#     출력 스크립트가 프로젝트 안에 있으면 프로젝트를 옮겨도 상대 위치가 같을 때
#     그대로 동작합니다.
#
# 하는 일
#   1) aws, tar, gzip이 PATH에 있는지 확인합니다. 없으면 설치 안내를 출력하고 종료합니다.
#   2) 받을 폴더가 프로젝트 안에 있고 심볼릭 링크가 아닌지 확인합니다.
#   3) aws s3 cp로 .tar.gz를 임시 파일로 내려받고 gzip -t로 무결성을 확인합니다.
#   4) tar -tzf와 tar -tvzf로 항목을 나열해 절대 경로, .. 경로, 밖을 가리키는
#      링크(심볼릭·하드 링크), 지원하지 않는 파일 형식을 거부합니다.
#   5) 빈 임시 폴더에만 압축을 풀고, 추출된 심볼릭 링크가 임시 폴더 밖을
#      가리키지 않는지 다시 확인합니다.
#   6) 받을 폴더에 심볼릭 링크가 있으면 중단하고, 검증된 파일만 복사합니다.
#   7) 원본에 없는 받을 폴더의 기존 파일은 삭제하지 않습니다.
#   8) 임시 파일과 임시 폴더는 종료 시 정리합니다. S3 원격은 변경하지 않습니다.
#
# 변경되는 파일
#   - 검증을 통과한 파일만 받을 폴더에 추가하거나 같은 이름으로 갱신합니다.
#   - 원본에 없는 받을 폴더의 기존 파일은 삭제하지 않습니다.
#   - 임시 파일/임시 폴더는 스크립트 종료 시 삭제합니다.
#   - S3 원격과 이 스크립트 파일은 실행 중 변경하지 않습니다.
#
# 전제 조건
#   - 실행 시 aws CLI와 tar, gzip이 PATH에 있어야 합니다.
#     이 스크립트는 도구를 자동 설치하지 않습니다.
#   - 기존 AWS CLI 구성(환경 변수, ~/.aws/config·credentials, SSO, 역할 등)을 그대로 사용합니다.
#     aws configure를 실행하거나 자격 증명을 저장·출력하지 않습니다.
#   - S3 원본에는 사용자 이름/비밀번호/토큰/미리 서명된 쿼리를 넣을 수 없습니다.
#   - AWS CLI가 출력하는 오류 메시지는 사용자의 AWS CLI 출력이며 dcraft가 제어하지 않습니다.
#   - tar.gz의 파일 이름에 " -> "가 들어 있으면 하드 링크로 보고 거부합니다.
#
# 실패 시
#   - 메시지를 출력하고 0이 아닌 코드로 종료합니다. 사전(pre) 훅이면 dcraft가
#     본 작업(up/update)을 시작하지 않습니다.
#   - 다운로드·무결성·경로·링크 검증 실패 시 받을 폴더는 변경되지 않고
#     임시 파일/폴더는 정리됩니다.
#   - 복사 도중 실패하면 받을 폴더에 일부 파일이 남아 있을 수 있습니다.
#     dcraft는 자동으로 되돌리지 않습니다.
#
# 수정 방법
#   - SRC, DEST, PROFILE 값을 직접 고치거나 dcraft generate-s3-hook로 다시 생성하세요.
#   - 이 파일의 내용이 바뀌면 dcraft의 스크립트 신뢰(script_trust) 승인을 다시 받아야 합니다.
#
# 다음 단계 (자동 등록·자동 실행 아님)
#   dcraft hooks --add --stage pre --script {{SCRIPT_ARG}}
#   dcraft up --approve script_trust   (또는 대화형 화면에서 신뢰 승인)
# =============================================================================
set -euo pipefail

SRC={{SOURCE_SH}}
PROFILE={{PROFILE_SH}}
SELF_DIR=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
{{PROJECT_DIR_EXPR}}
{{DEST_EXPR}}

say() { printf '%s\n' "$*"; }
die() { printf '오류: %s\n' "$*" >&2; exit 1; }

command -v aws >/dev/null 2>&1 || die "aws CLI를 찾지 못했습니다. AWS CLI를 설치한 뒤 PATH를 확인하세요. (이 스크립트는 도구를 자동 설치하지 않습니다.)"
command -v tar >/dev/null 2>&1 || die "tar를 찾지 못했습니다. tar를 설치한 뒤 PATH를 확인하세요. (이 스크립트는 도구를 자동 설치하지 않습니다.)"
command -v gzip >/dev/null 2>&1 || die "gzip을 찾지 못했습니다. gzip을 설치한 뒤 PATH를 확인하세요. (이 스크립트는 도구를 자동 설치하지 않습니다.)"

check_source() {
  [ -n "$SRC" ] || die "S3 원본 값이 비어 있습니다. 이 파일의 SRC를 확인하세요."
  case "$SRC" in
    s3://*) ;;
    *) die "S3 원본은 s3://bucket/... 형식이어야 합니다. 이 파일의 SRC를 확인하세요." ;;
  esac
  case "$SRC" in
    *"?"*|*"#"*) die "S3 원본에 쿼리 문자열이나 미리 서명된 URL을 사용할 수 없습니다. 기존 AWS CLI 자격 증명을 사용하세요." ;;
  esac
  authority=${SRC#s3://}
  authority=${authority%%/*}
  case "$authority" in
    ''|*@*) die "S3 원본의 버킷/인증 정보 형식이 올바르지 않습니다. 기존 AWS CLI 자격 증명을 사용하세요." ;;
  esac
  case "$SRC" in
    *.tar.gz) ;;
    *) die "이 스크립트는 tar 방식입니다. .tar.gz 객체 경로가 필요합니다. 이 파일의 SRC를 확인하세요." ;;
  esac
}

validate_destination() {
  local rel part cur
  [ -d "$PROJECT_DIR" ] || die "프로젝트 폴더를 찾지 못했습니다: $PROJECT_DIR"
  case "$DEST" in
    "$PROJECT_DIR"/*) ;;
    *) die "받을 폴더는 프로젝트 폴더 안이어야 합니다. 이 파일의 DEST를 확인하세요." ;;
  esac
  [ "$DEST" != "$PROJECT_DIR" ] || die "받을 폴더가 프로젝트 폴더 자체입니다. 프로젝트 안의 별도 폴더를 사용하세요."
  rel=${DEST#"$PROJECT_DIR"/}
  cur=$PROJECT_DIR
  while [ -n "$rel" ]; do
    case "$rel" in
      */*) part=${rel%%/*}; rel=${rel#*/} ;;
      *) part=$rel; rel='' ;;
    esac
    case "$part" in
      ''|'.') continue ;;
      '..') die "받을 폴더 경로에 ..를 사용할 수 없습니다. 이 파일의 DEST를 확인하세요." ;;
    esac
    cur="$cur/$part"
    if [ -L "$cur" ]; then
      die "받을 폴더 경로에 심볼릭 링크가 있습니다. 심볼릭 링크 너머로 쓰지 않습니다: $cur"
    fi
  done
  if [ -L "$DEST" ]; then
    die "받을 폴더가 심볼릭 링크입니다. 심볼릭 링크 너머로 쓰지 않습니다: $DEST"
  fi
  if [ -e "$DEST" ] && [ ! -d "$DEST" ]; then
    die "받을 폴더 경로에 폴더가 아닌 파일이 있습니다. 덮어쓰지 않습니다: $DEST"
  fi
  case "$SELF_DIR" in
    "$DEST"|"$DEST"/*) die "받을 폴더가 스크립트 폴더를 포함합니다. 배포 파일이 스크립트를 덮을 수 있어 중단합니다." ;;
  esac
  if [ -d "$DEST" ]; then
    DEST_REAL=$(CDPATH= cd -- "$DEST" && pwd -P) || die "받을 폴더의 실제 경로를 확인하지 못했습니다: $DEST"
    case "$DEST_REAL" in
      "$PROJECT_DIR"|"$PROJECT_DIR"/*) ;;
      *) die "받을 폴더가 프로젝트 폴더 밖을 가리킵니다. 심볼릭 링크를 확인하세요: $DEST" ;;
    esac
    if [ -n "$(find "$DEST" -type l -print -quit 2>/dev/null)" ]; then
      die "받을 폴더 안에 심볼릭 링크가 있습니다. 심볼릭 링크 너머로 복사하지 않습니다: $DEST"
    fi
  fi
}

prepare_destination() {
  validate_destination
  mkdir -p -- "$DEST" || die "받을 폴더를 만들지 못했습니다: $DEST"
  DEST_REAL=$(CDPATH= cd -- "$DEST" && pwd -P) || die "받을 폴더의 실제 경로를 확인하지 못했습니다: $DEST"
  case "$DEST_REAL" in
    "$PROJECT_DIR"|"$PROJECT_DIR"/*) ;;
    *) die "받을 폴더가 프로젝트 폴더 밖을 가리킵니다. 심볼릭 링크를 확인하세요: $DEST" ;;
  esac
  case "$SELF_DIR" in
    "$DEST_REAL"|"$DEST_REAL"/*) die "받을 폴더가 스크립트 폴더를 포함합니다. 배포 파일이 스크립트를 덮을 수 있어 중단합니다." ;;
  esac
  if [ -n "$(find "$DEST" -type l -print -quit 2>/dev/null)" ]; then
    die "받을 폴더 안에 심볼릭 링크가 있습니다. 심볼릭 링크 너머로 복사하지 않습니다: $DEST"
  fi
}

check_source
validate_destination

WORK=$(mktemp -d "${TMPDIR:-/tmp}/dcraft-s3-hook.XXXXXX") || die "임시 폴더를 만들지 못했습니다. TMPDIR과 남은 공간을 확인하세요."
cleanup() {
  case "${WORK:-}" in
    '') return 0 ;;
  esac
  rm -rf -- "$WORK" 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM
ARCHIVE=$WORK/artifact.tar.gz
STAGE=$WORK/stage
mkdir -m 700 -- "$STAGE" || die "빈 임시 폴더를 만들지 못했습니다: $STAGE"

say "S3 원본: $SRC"
say "받을 폴더: $DEST"
say "임시 파일로 내려받아 무결성과 경로를 검증한 뒤 복사합니다."
if [ -n "$PROFILE" ]; then
  say "AWS CLI 프로필: $PROFILE (기존 AWS CLI 구성)"
  if ! aws s3 cp "$SRC" "$ARCHIVE" --profile "$PROFILE" --only-show-errors --no-progress; then
    die "다운로드에 실패했습니다. 받을 폴더는 변경하지 않았습니다. 임시 파일은 정리합니다. AWS CLI 오류 메시지는 사용자의 AWS CLI가 출력한 것입니다."
  fi
else
  if ! aws s3 cp "$SRC" "$ARCHIVE" --only-show-errors --no-progress; then
    die "다운로드에 실패했습니다. 받을 폴더는 변경하지 않았습니다. 임시 파일은 정리합니다. AWS CLI 오류 메시지는 사용자의 AWS CLI가 출력한 것입니다."
  fi
fi
[ -s "$ARCHIVE" ] || die "내려받은 파일이 비어 있습니다. 받을 폴더는 변경하지 않았습니다."
if ! gzip -t "$ARCHIVE"; then
  die "gzip 무결성 검사에 실패했습니다(잘린 파일일 수 있음). 받을 폴더는 변경하지 않았습니다."
fi

ENTRIES=$WORK/entries.txt
if ! tar -tzf "$ARCHIVE" > "$ENTRIES" 2>/dev/null; then
  die "tar 항목 목록을 읽지 못했습니다. 아카이브 형식을 확인하세요. 받을 폴더는 변경하지 않았습니다."
fi
[ -s "$ENTRIES" ] || die "아카이브에 항목이 없습니다. 받을 폴더는 변경하지 않았습니다."

validate_entry() {
  local entry check rest part
  entry=$1
  case "$entry" in
    /*) die "아카이브에 절대 경로 항목이 있습니다. 받지 않습니다." ;;
  esac
  check=$entry
  case "$check" in
    */) check=${check%/} ;;
  esac
  [ -n "$check" ] || die "아카이브에 빈 경로 항목이 있습니다. 받지 않습니다."
  rest=$check
  while [ -n "$rest" ]; do
    case "$rest" in
      */*) part=${rest%%/*}; rest=${rest#*/} ;;
      *) part=$rest; rest='' ;;
    esac
    case "$part" in
      '') die "아카이브 항목에 빈 경로 구성 요소가 있습니다. 받지 않습니다." ;;
      '..') die "아카이브 항목에 .. 경로가 있습니다. 받지 않습니다." ;;
    esac
  done
}
while IFS= read -r entry; do
  validate_entry "$entry"
done < "$ENTRIES"

VERBOSE=$WORK/entries.verbose
if ! tar -tvzf "$ARCHIVE" > "$VERBOSE" 2>/dev/null; then
  die "tar 항목 정보를 읽지 못했습니다. 받을 폴더는 변경하지 않았습니다."
fi
while IFS= read -r line; do
  [ -n "$line" ] || continue
  kind=${line%"${line#?}"}
  case "$kind" in
    -|d|l) ;;
    h) die "아카이브에 하드 링크 항목이 있습니다. 밖을 가리키는 링크를 막기 위해 하드 링크는 받지 않습니다." ;;
    *) die "아카이브에 지원하지 않는 파일 형식(장치·FIFO·소켓 등)이 있습니다. 받지 않습니다." ;;
  esac
  case "$line" in
    *" -> "*)
      if [ "$kind" = "-" ]; then
        die "아카이브에 하드 링크 항목이 있습니다. 밖을 가리키는 링크를 막기 위해 하드 링크는 받지 않습니다."
      fi
      ;;
  esac
done < "$VERBOSE"

if ! tar -xzf "$ARCHIVE" -C "$STAGE"; then
  die "압축 해제에 실패했습니다. 받을 폴더는 변경하지 않았습니다. 임시 폴더는 정리합니다."
fi

target_inside() {
  local link target dir rel rest part depth
  link=$1
  target=$2
  dir=$(dirname -- "$link")
  rel=${dir#"$STAGE"}
  depth=0
  rest=$rel
  while [ -n "$rest" ]; do
    case "$rest" in
      */*) part=${rest%%/*}; rest=${rest#*/} ;;
      *) part=$rest; rest='' ;;
    esac
    case "$part" in
      ''|'.') ;;
      *) depth=$((depth + 1)) ;;
    esac
  done
  rest=$target
  while [ -n "$rest" ]; do
    case "$rest" in
      */*) part=${rest%%/*}; rest=${rest#*/} ;;
      *) part=$rest; rest='' ;;
    esac
    case "$part" in
      ''|'.') ;;
      '..') depth=$((depth - 1)); if [ "$depth" -lt 0 ]; then return 1; fi ;;
      *) depth=$((depth + 1)) ;;
    esac
  done
  return 0
}

TREE=$WORK/tree.bin
if ! find "$STAGE" -print0 > "$TREE" 2>/dev/null; then
  die "압축 해제 결과를 확인하지 못했습니다. 받을 폴더는 변경하지 않았습니다."
fi
while IFS= read -r -d '' path; do
  case "$path" in
    *$'\n'*|*$'\r'*) die "아카이브 항목 이름에 줄바꿈/CR 문자가 있습니다. 받지 않습니다." ;;
  esac
  if [ -L "$path" ]; then
    target=$(readlink -- "$path") || die "심볼릭 링크 대상을 읽지 못했습니다. 받을 폴더는 변경하지 않았습니다."
    case "$target" in
      /*) die "절대 경로를 가리키는 심볼릭 링크가 있습니다. 받지 않습니다." ;;
    esac
    if ! target_inside "$path" "$target"; then
      die "임시 폴더 밖을 가리키는 심볼릭 링크가 있습니다. 받지 않습니다: $path -> $target"
    fi
  elif [ ! -f "$path" ] && [ ! -d "$path" ]; then
    die "지원하지 않는 파일 형식이 있습니다. 받지 않습니다: $path"
  fi
done < "$TREE"

prepare_destination
if ! cp -R "$STAGE"/. "$DEST"/; then
  die "검증된 파일을 받을 폴더로 복사하지 못했습니다. 일부 파일이 남아 있을 수 있습니다. dcraft는 자동으로 되돌리지 않습니다."
fi
say "완료: $DEST"
say "원본에 없는 받을 폴더의 기존 파일은 삭제하지 않았습니다."
say "임시 파일과 임시 폴더는 이 스크립트 종료 시 정리됩니다."
`
