package tui

// Korean user-facing TUI text.
const (
	msgApp                  = "dcraft"
	msgWorking              = "작업을 실행하는 중…"
	msgPlanning             = "작업 계획을 확인하는 중…"
	msgApprovalConfirmTitle = "승인 확인"
	msgMenu                 = "작업"
	msgHintSingle           = "r 새로고침 · q 종료"
	msgHintMenu             = "↑/↓ 선택 · enter 실행 · r 새로고침 · q 종료"
	msgHintConfirm          = "enter 승인 · esc 취소 · q 종료"
	msgHintQuit             = "q 종료"
	msgNoPrimary            = "대화형 화면을 열 수 없습니다: 등록된 기본 화면이 없습니다."
	msgNoEngine             = "작업을 실행할 준비가 되지 않았습니다."
	msgNoEngineAction       = "dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."
	msgMenuMoreAbove        = "  … 위에 더 있음"
	msgMenuMoreBelow        = "  … 아래에 더 있음"
	msgViewTruncated        = "… 화면 높이에 맞춰 일부 내용을 생략했습니다. 전체 내용은 dcraft status로 확인하세요."

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
	msgHintForm        = "tab 필드 이동 · ↑/↓ 선택 · enter 다음/실행 · esc 취소"

	// Live screens. Closing a screen only detaches; it never stops services.
	msgLiveStreaming = "실시간 로그 (서비스는 계속 실행 중)"
	msgLiveAttached  = "서비스에 접속했습니다."
	msgLiveService   = "서비스"
	msgLiveCommand   = "명령"
	msgHintLive      = "q/esc/ctrl+c 화면 닫기 · 서비스는 계속 실행"
	msgHintAttach    = "ctrl+] 접속 종료 · 명령이 끝나면 자동 복귀"
	msgLiveStopped   = "실시간 화면을 닫았습니다. 서비스는 계속 실행 중입니다."
	msgLastOutput    = "마지막 실시간 출력"
)

const (
	msgWizardLoading       = "대화형 설정 단계를 준비하는 중…"
	msgWizardProgress      = "단계"
	msgWizardInputLabel    = "> "
	msgWizardNeedValue     = "값을 입력한 뒤 enter를 누르세요."
	msgWizardNeedChoice    = "↑/↓로 항목을 이동하고 enter로 선택하세요. 여러 항목은 space로 함께 선택할 수 있습니다."
	msgWizardPreviewFailed = "입력을 저장할 수 없습니다. 이전 단계로 돌아가 수정하세요."
	msgWizardPayload       = "대화형 설정 단계를 표시할 수 없습니다."
	msgWizardPayloadAction = "dcraft secrets 명령으로 참조를 직접 추가하세요."
	msgWizardUnavailable   = "이 기능은 대화형 설정을 지원하지 않습니다."
	msgWizardCanceled      = "대화형 설정을 취소했습니다. 저장된 내용은 없습니다."
	msgHintWizardChoice    = "↑/↓ 선택 · enter 다음 · ← 이전 · esc 취소 · q 종료"
	msgHintWizardMulti     = "↑/↓ 이동 · space 선택/해제 · enter 다음 · ← 이전 · esc 취소 · q 종료"
	msgHintWizardInput     = "입력 후 enter · backspace 수정 · ← 이전 · esc 취소 · q 종료"
	msgHintWizardPreview   = "enter 저장 · ← 이전 · esc 취소 · q 종료"
	msgHintWizardInvalid   = "← 이전 · esc 취소 · q 종료"
	msgHintWizardLoading   = "esc 취소 · q 종료"
)
