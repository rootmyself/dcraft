package hooks

// Korean user-facing text for the hooks command. Command names and machine
// values stay English.
const (
	msgTitle   = "호스트 스크립트"
	msgSummary = "up/update 전후에 실행할 로컬 스크립트를 명시적으로 등록합니다."
	msgUsage   = "dcraft hooks [--list|--add|--remove] [--stage pre|post] [--script 경로]\n\n" +
		"등록한 스크립트만 up/update 전후에 실행합니다. 파일명만으로는 실행하지 않습니다.\n" +
		"처음 실행하거나 내용이 바뀌면 별도의 스크립트 신뢰 승인이 필요합니다.\n" +
		"스크립트는 사용자 권한으로 프로젝트 폴더에서 실행되며, dcraft는 샌드박싱하거나 자동으로 되돌리지 않습니다."

	msgFlagList   = "등록된 스크립트와 신뢰 상태를 표시합니다 (기본)."
	msgFlagAdd    = "스크립트를 등록합니다."
	msgFlagRemove = "등록된 스크립트를 제거합니다."
	msgFlagStage  = "실행 시점: pre 또는 post (up/update 전후)"
	msgFlagScript = "프로젝트 기준 상대 경로 또는 절대 로컬 경로"

	msgStageRegister = "등록 저장"
	msgStageRemove   = "등록 제거"
	msgStageList     = "등록 확인"

	msgActionConflict  = "한 번에 하나의 동작만 지정하세요: --list, --add, --remove"
	msgMissingInput    = "필요한 입력이 없습니다: %s"
	msgUnknownStage    = "지원하지 않는 실행 시점입니다: %s (pre 또는 post)"
	msgNoService       = "스크립트 등록 연결이 준비되지 않았습니다."
	msgNoServiceAction = "dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."
	msgInputActionOne  = "dcraft hooks --help로 입력 형식을 확인하세요."
	msgInputActionTwo  = "경로와 실행 시점(pre/post)을 확인하세요."

	msgAdded        = "스크립트를 등록했습니다. 등록과 신뢰 승인은 별개이며, 다음 up/update에서 경로·시점·내용 식별자를 확인한 뒤 승인해야 합니다."
	msgRemoved      = "스크립트 등록을 제거했습니다."
	msgNoHooks      = "등록된 호스트 스크립트가 없습니다. up/update는 스크립트를 실행하지 않습니다."
	msgTrustPending = "아직 승인되지 않았거나 내용이 바뀐 스크립트가 있습니다. up/update 전에 별도 스크립트 신뢰 승인이 필요합니다."
	msgDisclosure   = "호스트 스크립트는 up/update에서만 실행됩니다. 사용자 권한으로 프로젝트 폴더에서 실행되며, dcraft는 샌드박싱하거나 자동으로 되돌리지 않습니다."

	msgWizardTitle        = "호스트 스크립트 등록"
	msgWizardActionTitle  = "동작 선택"
	msgWizardActionPrompt = "등록할지 제거할지 선택하세요."
	msgWizardAddLabel     = "스크립트 등록"
	msgWizardRemoveLabel  = "등록 제거"
	msgWizardStageTitle   = "실행 시점 선택"
	msgWizardStagePrompt  = "up/update의 어느 시점에 실행할지 선택하세요."
	msgWizardPreLabel     = "사전 (본 작업 전)"
	msgWizardPostLabel    = "사후 (본 작업 완료 후)"
	msgWizardScriptTitle  = "스크립트 경로 입력"
	msgWizardScriptPrompt = "프로젝트 기준 상대 경로 또는 절대 로컬 경로를 입력하세요."
	msgWizardScriptHint   = "예: scripts/pre-deploy.sh · hooks/migrate.sh · ./scripts/check-db.sh"
	msgWizardPreviewTitle = "다음 등록을 적용할까요?"

	msgWizardWorkdirPrefix = "실행 위치: "
	msgWizardWorkdir       = "프로젝트 폴더"
	msgWizardMissing       = "스크립트 파일을 찾지 못했습니다: %s"
	msgWizardDirectory     = "스크립트 경로가 폴더입니다: %s"
	msgWizardNotExecutable = "실행 권한이 없습니다. 등록할 수는 있지만 실행 전에 chmod +x가 필요합니다."
	msgWizardNotRegistered = "등록된 스크립트를 찾지 못했습니다: %s (%s)"

	msgStagePreLabel  = "사전 (up/update 전)"
	msgStagePostLabel = "사후 (up/update 후)"

	msgPresentConfig     = "설정 파일"
	msgPresentAction     = "동작"
	msgPresentActionList = "목록 확인"
	msgPresentList       = "등록된 스크립트:"
	msgPresentEffective  = "실제 경로"
	msgPresentContent    = "내용 식별자"
	msgPresentTrust      = "신뢰 상태"
	msgPresentProblem    = "문제"
	msgTrustTrusted      = "승인됨"
	msgTrustChanged      = "내용 변경됨 — 새 승인 필요"
	msgTrustNeeded       = "새 승인 필요"
	msgTrustUnavailable  = "실행할 수 없음"
)
