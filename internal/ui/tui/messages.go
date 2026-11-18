package tui

// Korean user-facing TUI text.
const (
	msgApp            = "dcraft"
	msgWorking        = "작업을 실행하는 중…"
	msgPlanning       = "작업 계획을 확인하는 중…"
	msgConfirmTitle   = "승인 확인"
	msgMenu           = "작업"
	msgHintSingle     = "r 새로고침 · q 종료"
	msgHintMenu       = "↑/↓ 선택 · enter 실행 · r 새로고침 · q 종료"
	msgHintConfirm    = "enter 승인 · esc 취소 · q 종료"
	msgHintQuit       = "q 종료"
	msgNoPrimary      = "대화형 화면을 열 수 없습니다: 등록된 기본 화면이 없습니다."
	msgNoEngine       = "작업을 실행할 준비가 되지 않았습니다."
	msgNoEngineAction = "dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."

	msgApprovalDeclined       = "승인하지 않아 아무것도 실행하지 않았습니다. 변경된 내용은 없습니다."
	msgApprovalDeclinedAction = "필요할 때 다시 실행해 계획을 확인하고 승인하세요."

	msgEnvChecking      = "실행환경을 확인하는 중…"
	msgEnvSelectTitle   = "실행환경 선택"
	msgEnvSelectIntro   = "이 폴더에는 이름 붙인 실행환경이 있습니다. 작업할 실행환경을 직접 선택하세요."
	msgEnvIsolationNote = "실행환경은 Compose 입력과 dcraft 상태·이력만 구분합니다. 외부 볼륨·bind mount·네트워크·고정 호스트 포트는 공유될 수 있습니다."
	msgHintEnvSelect    = "↑/↓ 선택 · enter 실행 · q 종료"
	msgEnvDefaultFiles  = "기본 Compose 파일"
	msgEnvComposeFiles  = "Compose 파일"
	msgEnvEnvFiles      = "환경 파일"
	msgEnvProfiles      = "프로필"
)
