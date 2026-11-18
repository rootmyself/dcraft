package tui

// Korean user-facing TUI text.
const (
	msgApp            = "dcraft"
	msgWorking        = "작업을 실행하는 중…"
	msgMenu           = "작업"
	msgHintSingle     = "r 새로고침 · q 종료"
	msgHintMenu       = "↑/↓ 선택 · enter 실행 · r 새로고침 · q 종료"
	msgHintQuit       = "q 종료"
	msgNoPrimary      = "대화형 화면을 열 수 없습니다: 등록된 기본 화면이 없습니다."
	msgNoEngine       = "작업을 실행할 준비가 되지 않았습니다."
	msgNoEngineAction = "dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."

	msgEnvChecking      = "실행환경을 확인하는 중…"
	msgEnvSelectTitle   = "실행환경 선택"
	msgEnvSelectIntro   = "이 폴더에는 이름 붙인 실행환경이 있습니다. 작업할 실행환경을 직접 선택하세요."
	msgEnvIsolationNote = "실행환경은 Compose 입력과 dcraft 상태·이력만 구분합니다. 외부 볼륨·bind mount·네트워크·고정 호스트 포트는 공유될 수 있습니다."
	msgHintEnvSelect    = "↑/↓ 선택 · enter 실행 · q 종료"
	msgEnvDefaultFiles  = "기본 Compose 파일"
	msgEnvComposeFiles  = "Compose 파일"
	msgEnvEnvFiles      = "환경 파일"
	msgEnvProfiles      = "프로필"

	// Mutating features ask once before changing the stack. This is not an
	// approval framework: the engine still owns every safety rule.
	msgConfirmTitle       = "다음 작업을 실행할까요?"
	msgConfirmActionLabel = "작업"
	msgConfirmTargetLabel = "대상"
	msgConfirmTargetNow   = "현재 폴더"
	msgConfirmEnvironment = "실행환경"
	msgConfirmFileLabel   = "Compose 파일"
	msgConfirmProfile     = "프로필"
	msgConfirmProject     = "프로젝트 이름"
	msgConfirmProjectAuto = "실행 후 표시 (자동 배정 또는 Compose 설정 이름)"
	msgConfirmScopeLabel  = "범위"
	msgConfirmScope       = "스택 전체 (서비스 선택은 CLI의 -s/--service를 사용하세요)"
	msgConfirmEffectLabel = "효과"
	msgConfirmRisk        = "이 작업은 스택을 변경합니다. 중단하면 일부 변경이 남을 수 있습니다."
	msgConfirmHints       = "enter 실행 · esc 취소 · q 종료"
	msgBusyQuit           = "작업이 진행 중입니다. 지금 화면을 닫으면 일부 변경이 남을 수 있습니다. 완료된 뒤 종료하거나, 중단하려면 ctrl+c를 누르세요."
	msgCanceling          = "중단을 요청했습니다. 진행 중인 작업이 안전하게 끝날 때까지 기다리세요. 일부 변경이 남을 수 있습니다."
	msgHintBusy           = "ctrl+c 중단 요청 · 작업 완료 후 q 종료"
)
