package tui

const (
	msgEnvActionsTitle = "실행환경"
	msgEnvNewAlias     = "새 별칭"
	msgEnvSwitch       = "전환"
	msgEnvCurrent      = " (현재)"
	msgEnvReloadFailed = "실행환경 목록 불러오기 실패: "
	msgEnvSwitchFailed = "전환할 실행환경 불러오기 실패."
)

// Korean user-facing TUI text.
const (
	msgListEditing          = " (수정 중)"
	msgListHint             = "%d개 · ↑↓ 선택 · enter 추가/수정/완료 · d 삭제 · esc 수정 취소/마법사 취소"
	msgListCancel           = "마법사 취소"
	msgListCancelEdit       = "수정 취소"
	msgListSelect           = "선택"
	msgListEnter            = "추가/완료"
	msgWorking              = "작업을 실행하는 중…"
	msgPlanning             = "작업 계획을 확인하는 중…"
	msgApprovalConfirmTitle = "승인 확인"
	msgMenu                 = "작업"
	msgKeyMenu              = "메뉴"
	msgNoPrimary            = "대화형 화면 열기 실패: 등록된 기본 화면 없음."
	msgNoEngine             = "작업을 실행할 준비가 되지 않았음."
	msgNoEngineAction       = "dcraft를 다시 빌드하거나 설치한 뒤 실행."
	msgMenuMoreAbove        = "  %s 위에 더 있음"
	msgMenuMoreBelow        = "  %s 아래에 더 있음"
	msgDetailTitle          = "전체 값"
	// Navigation. The section headings come from core.Section.KoreanLabel, so
	// only the frame around them is text owned by the TUI.
	msgNavTitle      = "기능 · %d"
	msgNavEmpty      = "조건에 맞는 기능 없음. /로 다른 낱말 검색 · esc로 해제."
	msgStageTimeline = "단계"
	msgStagePending  = "예정"

	msgApprovalDeclined       = "승인하지 않아 아무것도 실행하지 않았음. 변경 내용 없음."
	msgApprovalDeclinedAction = "필요할 때 다시 실행해 계획을 확인하고 승인."

	msgEnvChecking      = "실행환경을 확인하는 중…"
	msgEnvSelectTitle   = "실행환경 선택"
	msgEnvSelectIntro   = "이 폴더에 이름 붙인 실행환경 있음. 작업할 실행환경 선택."
	msgEnvIsolationNote = "실행환경은 Compose 입력과 dcraft 상태·이력만 구분. 외부 볼륨·bind mount·네트워크·고정 호스트 포트는 공유 가능."
	msgEnvDefaultFiles  = "기본 Compose 파일"
	msgEnvComposeFiles  = "Compose 파일"
	msgEnvEnvFiles      = "환경 파일"
	msgEnvProfiles      = "프로필"

	// Mutating features ask once before changing the stack. This is not an
	// approval framework: the engine still owns every safety rule.
	msgConfirmTitle       = "작업 실행 확인"
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
	msgConfirmRisk        = "스택을 변경하는 작업. 중단하면 일부 변경이 남을 수 있음."
	// The panel's non-color warning channel and the section heading for the
	// independent approvals. Both are labels, not new instructions.
	msgConfirmRiskLabel = "위험"
	msgApprovalsSection = "필요한 승인"
	// The confirmation panel's pinned button row. The brackets are drawn by
	// the panel; these are the labels only.
	msgModalApprove = "승인"
	msgModalCancel  = "취소"
	msgHelpTitle    = "도움말"
	msgHelpClose    = "닫기"
	msgBusyQuit     = "작업 진행 중. 지금 화면을 닫으면 일부 변경이 남을 수 있음. 완료된 뒤 종료하거나, 중단하려면 ctrl+c."
	msgCanceling    = "중단 요청됨. 진행 중인 작업이 안전하게 끝날 때까지 대기. 일부 변경이 남을 수 있음."

	// Interactive inputs are collected only where a feature declares them
	// (exec). The TUI renders labels and choices; the feature owns validation.
	msgLoadingInputs   = "실행에 필요한 입력을 확인하는 중…"
	msgFormTitle       = "실행 전 확인"
	msgFormAction      = "작업"
	msgFormRunTitle    = "실행 내용"
	msgFormEmpty       = "(아직 입력 없음)"
	msgFormRequired    = "%s 항목 입력."
	msgFormCanceled    = "입력 취소. 아무것도 실행하지 않았음."
	msgFormUnsupported = "이 작업은 대화형 입력을 지원하지 않음."

	// Generic service scope selection. The table is read-only and comes from
	// the same engine/status inspect path as the status screen. Its columns are
	// the status table's, prefixed with the selection column, in the approved
	// order.
	msgServiceLoading       = "서비스 목록을 확인하는 중…"
	msgServiceSelectTitle   = "서비스 선택"
	msgServiceSelectIntro   = "적용 범위 선택. 전체 선택 시 스택 전체에 적용."
	msgServiceSelectionNote = "선택은 같은 엔진 규칙으로 다시 검증됨. 인스턴스 단위 선택은 로그·exec 화면에서만 제공."
	msgServiceWhole         = "전체 (스택 전체)"
	msgServiceCanceled      = "서비스 선택 취소. 아무것도 실행하지 않았음."
	msgServiceNotConfigured = "현재 설정에 없음"
	msgServiceNoServices    = "서비스가 없음. Compose 파일에 services 항목이 있는지 확인."
	msgServiceNoPort        = "-"
	// State and health wording comes from internal/stack/present; the service
	// scope screen draws the same words as the status table.
	// Table headers. The status document owns the same words on its own
	// surface; the two screens must not drift.
	msgServiceColumnSelect    = "선택"
	msgServiceColumnService   = "서비스"
	msgServiceColumnState     = "상태"
	msgServiceColumnInstances = "인스턴스"
	msgServiceColumnHealth    = "health"
	msgServiceColumnImage     = "이미지"
	msgServiceColumnPort      = "포트"

	// Live screens. Closing a screen only detaches; it never stops services.
	msgLiveStreaming     = "실시간 로그 (서비스는 계속 실행 중)"
	msgLiveAttached      = "서비스 접속."
	msgLiveService       = "서비스"
	msgLiveInstance      = "인스턴스"
	msgLiveAllInstances  = "전체"
	msgLiveCommand       = "명령"
	msgLiveStopped       = "실시간 화면 닫음. 서비스는 계속 실행 중."
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
	msgWizardNeedValue         = "값 입력 후 enter."
	msgWizardNeedChoice        = "↑/↓ 이동 · enter 선택 · space 다중 선택."
	msgWizardPreviewFailed     = "입력 저장 불가. 이전 단계로 돌아가 수정."
	msgWizardPayload           = "대화형 설정 단계 표시 불가."
	msgWizardPayloadAction     = "dcraft --help로 명령과 옵션을 확인하거나 같은 작업을 CLI에서 다시 실행."
	msgWizardUnavailable       = "이 기능은 대화형 설정을 지원하지 않음."
	msgWizardCanceled          = "대화형 설정 취소. 저장된 내용 없음."
	msgWizardOptionalHint      = "비우려면 enter"
	msgWizardRequiredApprovals = msgApprovalsSection
	// The preview panel's own heading. It states the collected answers that
	// the apply will send, so the effect description below it is never the
	// only thing the reader has to trust.
	msgWizardAppliedTitle = "적용 내용"
	// An optional answer that was deliberately left empty is stated as such:
	// leaving the row out would make the review look incomplete.
	msgWizardAnswerEmpty = "(비움)"
)

// Keycap labels. The visual layer draws the keycap row; these are the words the
// TUI's own screens put in it, so no screen writes a Korean label inline.
const (
	msgKeyClear       = "해제"
	msgKeyCommit      = "확정"
	msgKeyDetach      = "접속 종료"
	msgKeyHelp        = "도움말"
	msgKeyCloseScreen = "화면 닫기"
	msgKeyStop        = "중단"
	msgKeyScroll      = "스크롤"
	msgKeyEnds        = "처음/끝"
	msgKeyQuit        = "종료"
	msgKeySelect      = "선택"
	msgKeyRun         = "실행"
	msgKeyMove        = "이동"
	msgKeyToggle      = "선택/해제"
	msgKeyCancel      = "취소"
	msgKeySave        = "저장"
	msgKeyPrev        = "이전"
	msgKeyNext        = "다음"
	msgKeyNextSkip    = "다음/건너뛰기"
	msgKeyPage        = "페이지"
	msgKeyClose       = "닫기"
	msgKeyDetail      = "상세"

	// The environment summary counts its files.
	msgEnvCount = "%s %d개"

	// The header names a verified local Docker endpoint.
	msgLocalEndpoint = "로컬"
)
