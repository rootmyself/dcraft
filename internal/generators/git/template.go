package git

// gitScriptTemplate is the reviewed script body. It contains Korean guidance
// for a human maintainer and English commands. Placeholders are replaced by
// render; the body must stay valid for bash 3.2 (macOS) and modern Linux bash.
const gitScriptTemplate = `#!/usr/bin/env bash
# =============================================================================
# dcraft Git 호스트 스크립트 (dcraft generate-git-hook로 생성)
#
# 입력
#   - 저장소: {{REPO}}
#   - ref: {{REF}} (종류: {{KIND_LABEL}})
#   - 받을 폴더: {{DEST}}
#   - 실행 시점: {{STAGE_LABEL}}
#
# 실행 위치
#   - dcraft 훅으로 등록하면 프로젝트 폴더에서 실행됩니다.
#   - 직접 실행해도 이 스크립트는 자기 위치에서 프로젝트 폴더를 계산합니다.
#     프로젝트 폴더: {{PROJECT_DIR}}
#     출력 스크립트가 프로젝트 안에 있으면 프로젝트를 옮겨도 상대 위치가 같을 때
#     그대로 동작합니다. 출력 스크립트가 프로젝트 밖이면 위 경로가 파일에 고정됩니다.
#
# 하는 일
#   1) 받을 폴더가 아직 Git 저장소가 아니면 clone합니다.
#   2) 이미 같은 origin의 저장소면 fetch 후 요청한 ref를 checkout합니다.
#   3) 작업 트리에 커밋되지 않은 변경(추적/미추적)이 있으면 아무것도 바꾸지 않고 실패합니다.
#      dcraft가 마지막으로 받은 commit과 현재 HEAD가 다르면(로컬 commit/checkout)도 실패합니다.
#   4) 짧은 ref 이름은 실행할 때 원격에서 branch/tag를 확인합니다. 같은 이름이 둘 다
#      있으면 추측하지 않고 실패합니다. 전체 SHA는 그대로 fetch하며 짧은 SHA는 지원하지
#      않습니다. annotated tag는 commit으로 peel합니다.
#   5) git reset --hard, 강제 checkout, 폴더 삭제, 임의 경로 덮어쓰기, 도구 설치는 하지 않습니다.
#      checkout 전에 이전 HEAD를 refs/dcraft/previous에 보관해 커밋을 찾을 수 있게 합니다.
#
# 전제 조건
#   - git CLI가 PATH에 있어야 합니다. 이 스크립트는 Git을 자동 설치하지 않습니다.
#   - 인증은 기존 credential helper 또는 SSH agent를 사용합니다.
#     저장소 URL에 사용자 이름/비밀번호/토큰을 넣지 마세요. 이 파일에는 인증 정보가 없습니다.
#   - 전체 SHA fetch는 서버가 해당 커밋 fetch를 허용해야 합니다(예: GitHub).
#     서버가 거부하면 branch 또는 tag ref를 사용하세요.
#   - submodule은 자동으로 갱신하지 않습니다.
#   - AI 호출이나 별도 서비스 계정이 필요하지 않습니다. 저장소 접근은 사용자의 기존 Git 인증을 따릅니다.
#
# 실패 시
#   - 메시지를 출력하고 0이 아닌 코드로 종료합니다. 사전(pre) 훅이면 dcraft가
#     본 작업(up/update)을 시작하지 않습니다. 사후(post) 훅이면 본 작업은 이미
#     완료되었고 실패는 경고로 보고됩니다.
#   - 이미 받은 파일은 남아 있을 수 있으며 dcraft가 자동으로 되돌리지 않습니다.
#
# 수정 방법
#   - REPO, REF, REF_KIND, DEST 값을 직접 고치거나 dcraft generate-git-hook로 다시 생성하세요.
#   - 이 파일의 내용이 바뀌면 dcraft의 스크립트 신뢰(script_trust) 승인을 다시 받아야 합니다.
#
# 다음 단계 (자동 등록·자동 실행 아님)
#   dcraft hooks --add --stage {{STAGE}} --script {{SCRIPT_ARG}}
#   dcraft up --approve script_trust   (또는 대화형 화면에서 신뢰 승인)
# =============================================================================
set -euo pipefail

# SELF_DIR uses only bash builtins: a generated script must still reach its
# own guidance when PATH is empty (no dirname, no git). An external command
# inside a command substitution would fail silently here — command
# substitution does not trip "set -e" — and leave SELF_DIR empty.
case "${BASH_SOURCE[0]}" in
  */*) SELF_DIR=${BASH_SOURCE[0]%/*} ;;
  *)   SELF_DIR=. ;;
esac
SELF_DIR=$(CDPATH= cd -- "$SELF_DIR" && pwd -P)

REPO={{REPO_SH}}
REF={{REF_SH}}
REF_KIND={{KIND_SH}}
{{PROJECT_DIR_EXPR}}
{{DEST_EXPR}}

say() { printf '%s\n' "$*"; }
die() { printf '오류: %s\n' "$*" >&2; exit 1; }

command -v git >/dev/null 2>&1 || die "git CLI를 찾지 못했습니다. Git을 설치한 뒤 PATH를 확인하세요. (이 스크립트는 도구를 자동 설치하지 않습니다.)"

[ -n "$REPO" ] || die "저장소 값이 비어 있습니다. 이 파일의 REPO를 확인하세요."
case "$REPO" in
  -*) die "저장소 값은 -로 시작할 수 없습니다: $REPO" ;;
esac

check_repo_credentials() {
  case "$REPO" in
    *"://"*) ;;
    *) return 0 ;;
  esac
  scheme=${REPO%%://*}
  rest=${REPO#*://}
  authority=${rest%%/*}
  case "$authority" in
    *@*) ;;
    *) return 0 ;;
  esac
  userinfo=${authority%@*}
  case "$scheme" in
    http|https|HTTP|HTTPS)
      die "저장소 URL에 인증 정보가 들어 있습니다. 기존 credential helper 또는 SSH agent를 사용하고 URL에서는 인증 정보를 빼세요." ;;
  esac
  case "$userinfo" in
    *:*) die "저장소 URL에 비밀번호가 들어 있습니다. 기존 인증 수단을 사용하고 URL에서는 인증 정보를 빼세요." ;;
  esac
}
check_repo_credentials

is_full_sha() {
  case "$1" in
    *[!0-9a-fA-F]*) return 1 ;;
  esac
  case "${#1}" in
    40|64) return 0 ;;
    *) return 1 ;;
  esac
}

validate_ref_name() {
  local kind_value="$1"
  local name="$2"
  case "$name" in
    ''|-*) die "ref 이름이 비어 있거나 -로 시작합니다: $name" ;;
  esac
  case "$name" in
    *" "*|*"	"*) die "ref 이름에 공백이 있습니다: $name" ;;
  esac
  if [ "$kind_value" = "tag" ]; then
    git check-ref-format "refs/tags/$name" >/dev/null 2>&1 || die "유효하지 않은 tag 이름입니다: $name"
  else
    git check-ref-format "refs/heads/$name" >/dev/null 2>&1 || die "유효하지 않은 branch 이름입니다: $name"
  fi
}

KIND=''
if [ "$REF_KIND" = "commit" ]; then
  is_full_sha "$REF" || die "REF_KIND='commit'에는 40자(또는 64자) 전체 SHA가 필요합니다. 짧은 SHA는 지원하지 않습니다: $REF"
  KIND=commit
  REF=$(printf '%s' "$REF" | tr 'A-F' 'a-f')
elif [ "$REF_KIND" = "branch" ]; then
  case "$REF" in
    refs/heads/*) REF=${REF#refs/heads/} ;;
    refs/*) die "branch에는 refs/heads/... 또는 짧은 이름을 사용하세요: $REF" ;;
  esac
  validate_ref_name branch "$REF"
  KIND=branch
elif [ "$REF_KIND" = "tag" ]; then
  case "$REF" in
    refs/tags/*) REF=${REF#refs/tags/} ;;
    refs/*) die "tag에는 refs/tags/... 또는 짧은 이름을 사용하세요: $REF" ;;
  esac
  validate_ref_name tag "$REF"
  KIND=tag
elif [ "$REF_KIND" = "auto" ]; then
  if is_full_sha "$REF"; then
    KIND=commit
    REF=$(printf '%s' "$REF" | tr 'A-F' 'a-f')
  else
    case "$REF" in
    refs/heads/*)
      REF=${REF#refs/heads/}
      validate_ref_name branch "$REF"
      KIND=branch
      ;;
    refs/tags/*)
      REF=${REF#refs/tags/}
      validate_ref_name tag "$REF"
      KIND=tag
      ;;
    refs/*)
      die "지원하지 않는 전체 ref입니다. refs/heads/... 또는 refs/tags/...를 사용하세요: $REF"
      ;;
    *)
      if [[ "$REF" =~ ^[0-9a-fA-F]{4,39}$ ]]; then
        die "짧은 SHA는 지원하지 않습니다. 40자(또는 64자) 전체 SHA를 사용하세요: $REF"
      fi
      validate_ref_name branch "$REF"
      has_branch=0
      has_tag=0
      if git ls-remote --exit-code --heads "$REPO" "refs/heads/$REF" >/dev/null 2>&1; then
        has_branch=1
      else
        status=$?
        [ "$status" -eq 2 ] || die "원격 branch 확인에 실패했습니다(exit $status). 저장소 URL·인증·네트워크를 확인하세요."
      fi
      if git ls-remote --exit-code --tags "$REPO" "refs/tags/$REF" >/dev/null 2>&1; then
        has_tag=1
      else
        status=$?
        [ "$status" -eq 2 ] || die "원격 tag 확인에 실패했습니다(exit $status). 저장소 URL·인증·네트워크를 확인하세요."
      fi
      if [ "$has_branch" -eq 1 ] && [ "$has_tag" -eq 1 ]; then
        die "같은 이름의 branch와 tag가 모두 있습니다: $REF. refs/heads/$REF 또는 refs/tags/${REF}로 정확히 지정하세요."
      elif [ "$has_branch" -eq 1 ]; then
        KIND=branch
      elif [ "$has_tag" -eq 1 ]; then
        validate_ref_name tag "$REF"
        KIND=tag
      else
        die "원격에서 ref를 찾지 못했습니다: $REF (branch와 tag 모두 없음). ref 이름과 저장소를 확인하세요."
      fi
      ;;
    esac
  fi
else
  die "REF_KIND 값이 올바르지 않습니다: $REF_KIND (auto, branch, tag, commit)"
fi

[ -d "$PROJECT_DIR" ] || die "프로젝트 폴더를 찾지 못했습니다: $PROJECT_DIR"
DEST_PARENT=$(dirname -- "$DEST")
mkdir -p -- "$DEST_PARENT" || die "받을 폴더의 상위 폴더를 만들지 못했습니다: $DEST_PARENT"

if [ -e "$DEST" ] || [ -L "$DEST" ]; then
  [ -d "$DEST" ] || die "받을 폴더 경로에 폴더가 아닌 파일이 있습니다. 덮어쓰지 않습니다: $DEST"
  DEST_REAL=$(CDPATH= cd -- "$DEST" && pwd -P)
else
  DEST_REAL=$(CDPATH= cd -- "$DEST_PARENT" && pwd -P)/$(basename -- "$DEST")
fi
[ "$DEST_REAL" != "$PROJECT_DIR" ] || die "받을 폴더가 프로젝트 폴더 자체입니다. 프로젝트 안의 별도 폴더를 사용하세요."
case "$SELF_DIR" in
  "$DEST_REAL"|"$DEST_REAL"/*) die "받을 폴더가 스크립트 폴더를 포함합니다. checkout이 스크립트를 덮을 수 있어 중단합니다." ;;
esac

MODE=clone
if [ -d "$DEST" ] && [ -n "$(ls -A "$DEST" 2>/dev/null || true)" ]; then
  if git -C "$DEST" rev-parse --show-toplevel >/dev/null 2>&1; then
    ROOT=$(git -C "$DEST" rev-parse --show-toplevel)
    ROOT_REAL=$(CDPATH= cd -- "$ROOT" && pwd -P)
    if [ "$ROOT_REAL" = "$DEST_REAL" ]; then
      MODE=update
    else
      die "받을 폴더가 다른 Git 저장소 내부에 있고 비어 있지 않습니다. 기존 저장소를 건드리지 않습니다: $DEST"
    fi
  else
    die "받을 폴더가 비어 있지 않고 Git 저장소도 아닙니다. 기존 파일을 지우지 않습니다: $DEST"
  fi
fi

if [ "$MODE" = "update" ]; then
  origin=$(git -C "$DEST" remote get-url origin 2>/dev/null) || die "받을 폴더에 origin이 없습니다. 자동으로 지정하지 않습니다: $DEST"
  [ "$origin" = "$REPO" ] || die "받을 폴더의 origin이 생성 시 저장소와 다릅니다. 자동으로 바꾸지 않습니다. origin=$origin"
  dirty=$(git -C "$DEST" status --porcelain) || die "받을 폴더의 Git 상태를 확인하지 못했습니다: $DEST"
  if [ -n "$dirty" ]; then
    die "작업 트리에 커밋되지 않은 변경이 있습니다. 보존을 위해 아무것도 바꾸지 않고 중단합니다. 변경을 commit/stash/직접 정리한 뒤 다시 실행하세요: $DEST"
  fi
  # A managed destination records the last commit this script checked out. If
  # HEAD moved since then, it may be the user's local commit or checkout;
  # refuse instead of making it hard to find.
  GIT_DIR=$(git -C "$DEST" rev-parse --absolute-git-dir) || die "받을 폴더의 .git 경로를 확인하지 못했습니다: $DEST"
  MARKER="$GIT_DIR/dcraft-git-hook.head"
  if [ -f "$MARKER" ]; then
    LAST_SYNC=$(cat "$MARKER" 2>/dev/null) || die "마지막 동기화 커밋 기록을 읽지 못했습니다: $MARKER"
    CURRENT_HEAD=$(git -C "$DEST" rev-parse --verify --quiet HEAD 2>/dev/null || true)
    if [ -n "$CURRENT_HEAD" ] && [ "$CURRENT_HEAD" != "$LAST_SYNC" ]; then
      die "현재 HEAD가 dcraft가 마지막으로 받은 커밋과 다릅니다. 로컬 commit/checkout을 보존하기 위해 중단합니다. HEAD=$CURRENT_HEAD 마지막=$LAST_SYNC"
    fi
  fi
else
  git clone --quiet --depth 1 --no-checkout -- "$REPO" "$DEST" || die "clone에 실패했습니다. 저장소 URL·인증·네트워크·폴더 권한을 확인하세요: $DEST"
  GIT_DIR=$(git -C "$DEST" rev-parse --absolute-git-dir) || die "clone한 폴더의 .git 경로를 확인하지 못했습니다: $DEST"
  MARKER="$GIT_DIR/dcraft-git-hook.head"
fi

case "$KIND" in
  branch) REFSPEC="refs/heads/$REF" ;;
  tag) REFSPEC="refs/tags/$REF" ;;
  commit) REFSPEC="$REF" ;;
  *) die "내부 오류: 알 수 없는 ref 종류 $KIND" ;;
esac

git -C "$DEST" fetch --quiet --depth 1 origin "$REFSPEC" || die "fetch에 실패했습니다($REFSPEC). 인증·네트워크·서버의 ref 공개 정책을 확인하세요. 짧은 SHA는 사용할 수 없습니다."
COMMIT=$(git -C "$DEST" rev-parse --verify --quiet 'FETCH_HEAD^{commit}') || die "받은 ref에서 commit을 확인하지 못했습니다. annotated tag라면 서버가 tag를 제공하는지 확인하세요: $REF"
OLD_HEAD=$(git -C "$DEST" rev-parse --verify --quiet HEAD 2>/dev/null || true)
if [ -n "$OLD_HEAD" ]; then
  # Keep the previous commit reachable even when the destination was adopted
  # from an older setup. This is a ref write, not a checkout or reset.
  git -C "$DEST" update-ref refs/dcraft/previous "$OLD_HEAD" || say "경고: 이전 HEAD를 refs/dcraft/previous에 보관하지 못했습니다: $OLD_HEAD"
fi
git -C "$DEST" checkout --quiet --detach "$COMMIT" || die "checkout에 실패했습니다. 기존 파일은 그대로 남아 있을 수 있습니다. 충돌하는 미추적 파일을 확인하세요: $DEST"

HEAD_SHA=$(git -C "$DEST" rev-parse HEAD)
printf '%s\n' "$HEAD_SHA" > "$MARKER" || say "경고: 마지막 동기화 커밋 기록을 저장하지 못했습니다: $MARKER"
say "완료: $DEST"
say "커밋: $HEAD_SHA"
say "ref: $REF ($KIND)"
`
