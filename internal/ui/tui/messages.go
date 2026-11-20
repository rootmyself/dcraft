package tui

// Korean user-facing TUI text.
const (
	msgWorking              = "작업을 실행하는 중…"
	msgPlanning             = "작업 계획을 확인하는 중…"
	msgApprovalConfirmTitle = "승인 확인"
	msgMenu                 = "작업"
	msgNoPrimary            = "대화형 화면을 열 수 없습니다: 등록된 기본 화면이 없습니다."
	msgNoEngine             = "작업을 실행할 준비가 되지 않았습니다."
	msgNoEngineAction       = "dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."
	msgMenuMoreAbove        = "  %s 위에 더 있음"
	msgMenuMoreBelow        = "  %s 아래에 더 있음"
	msgDetailTitle          = "전체 값"
	// Navigation. The section headings come from core.Section.KoreanLabel, so
	// only the frame around them is text owned by the TUI.
	msgNavTitle      = "기능 · %d"
	msgNavEmpty      = "조건에 맞는 기능이 없습니다. /로 다른 낱말을 찾거나 esc로 해제하세요."
	msgStageTimeline = "단계"
	msgStagePending  = "예정"

	msgApprovalDeclined       = "승인하지 않아 아무것도 실행하지 않았습니다. 변경된 내용은 없습니다."
	msgApprovalDeclinedAction = "필요할 때 다시 실행해 계획을 확인하고 승인하세요."

	msgEnvChecking      = "실행환경을 확인하는 중…"
	msgEnvSelectTitle   = "실행환경 선택"
	msgEnvSelectIntro   = "이 폴더에는 이름 붙인 실행환경이 있습니다. 작업할 실행환경을 직접 선택하세요."
	msgEnvIsolationNote = "실행환경은 Compose 입력과 dcraft 상태·이력만 구분합니다. 외부 볼륨·bind mount·네트워크·고정 호스트 포트는 공유될 수 있습니다."
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
	msgConfirmScope       = "스택 전체"
	msgConfirmEffectLabel = "효과"
	msgConfirmRisk        = "이 작업은 스택을 변경합니다. 중단하면 일부 변경이 남을 수 있습니다."
	msgBusyQuit           = "작업이 진행 중입니다. 지금 화면을 닫으면 일부 변경이 남을 수 있습니다. 완료된 뒤 종료하거나, 중단하려면 ctrl+c를 누르세요."
	msgCanceling          = "중단을 요청했습니다. 진행 중인 작업이 안전하게 끝날 때까지 기다리세요. 일부 변경이 남을 수 있습니다."

	// Interactive inputs are collected only where a feature declares them
	// (exec). The TUI renders labels and choices; the feature owns validation.
	msgLoadingInputs   = "실행에 필요한 입력을 확인하는 중…"
	msgFormTitle       = "실행 전 확인"
	msgFormAction      = "작업"
	msgFormRunTitle    = "실행 내용"
	msgFormEmpty       = "(아직 입력 없음)"
	msgFormRequired    = "%s 항목을 입력하세요."
	msgFormCanceled    = "입력을 취소했습니다. 아무것도 실행하지 않았습니다."
	msgFormUnsupported = "이 작업은 대화형 입력을 지원하지 않습니다."

	// Generic service scope selection. The list is read-only and comes from
	// the same engine/status inspect path as the status screen.
	msgServiceLoading         = "서비스 목록을 확인하는 중…"
	msgServiceSelectTitle     = "서비스 선택"
	msgServiceSelectIntro     = "이 작업을 적용할 범위를 선택하세요. 전체를 선택하면 스택 전체에 적용합니다."
	msgServiceSelectionNote   = "선택은 같은 엔진 규칙으로 다시 검증됩니다. 인스턴스 단위 선택은 로그·exec 화면에서만 제공합니다."
	msgServiceWhole           = "전체 (스택 전체)"
	msgServiceCanceled        = "서비스 선택을 취소했습니다. 아무것도 실행하지 않았습니다."
	msgServiceNoInstances     = "인스턴스 없음"
	msgServiceInstances       = "인스턴스 %d개"
	msgServiceNotConfigured   = "현재 설정에 없음"
	msgServiceStateRunning    = "실행 중"
	msgServiceStateExited     = "중지됨"
	msgServiceStatePaused     = "일시정지됨"
	msgServiceStateRestarting = "재시작 중"
	msgServiceStateCreated    = "생성됨"
	msgServiceStateNotCreated = "컨테이너 없음"
	msgServiceStateUnknown    = "상태 알 수 없음"
	msgServiceHealthy         = "healthy"
	msgServiceHealthStarting  = "health 확인 중"
	msgServiceUnhealthy       = "unhealthy"
	msgServiceHealthNone      = "health 없음"

	// Live screens. Closing a screen only detaches; it never stops services.
	msgLiveStreaming     = "실시간 로그 (서비스는 계속 실행 중)"
	msgLiveAttached      = "서비스에 접속했습니다."
	msgLiveService       = "서비스"
	msgLiveInstance      = "인스턴스"
	msgLiveAllInstances  = "전체"
	msgLiveCommand       = "명령"
	msgLiveStopped       = "실시간 화면을 닫았습니다. 서비스는 계속 실행 중입니다."
	msgLiveKeepsRunning  = "서비스는 계속 실행 중"
	msgLiveDropped       = "오래된 줄 %d개 버림"
	msgLiveScrollKeys    = "PgUp/PgDn · g/G"
	msgLastOutput        = "마지막 실시간 출력"
	msgLastOutputOmitted = "마지막 실시간 출력 (앞 %d줄 생략)"
)

const (
	msgWizardLoading           = "대화형 설정 단계를 준비하는 중…"
	msgWizardProgress          = "단계"
	msgWizardInputLabel        = "> "
	msgWizardNeedValue         = "값을 입력한 뒤 enter를 누르세요."
	msgWizardNeedChoice        = "↑/↓로 항목을 이동하고 enter로 선택하세요. 여러 항목은 space로 함께 선택할 수 있습니다."
	msgWizardPreviewFailed     = "입력을 저장할 수 없습니다. 이전 단계로 돌아가 수정하세요."
	msgWizardPayload           = "대화형 설정 단계를 표시할 수 없습니다."
	msgWizardPayloadAction     = "dcraft --help로 명령과 옵션을 확인하거나 같은 작업을 CLI에서 다시 실행하세요."
	msgWizardUnavailable       = "이 기능은 대화형 설정을 지원하지 않습니다."
	msgWizardCanceled          = "대화형 설정을 취소했습니다. 저장된 내용은 없습니다."
	msgWizardOptionalHint      = "비우려면 enter"
	msgWizardRequiredApprovals = "필요한 승인"
)
