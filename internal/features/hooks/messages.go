package hooks

// Korean user-facing text for the hooks command. Command names and machine
// values stay English.
const (
	msgTitle   = "호스트 스크립트"
	msgSummary = "up/update 전후에 실행할 로컬 스크립트를 명시적으로 등록."
	// Menu labels. The group heading owns the generated-script features that
	// declare it as Parent; this feature's own entry is the "custom" child that
	// registers a script the project already has.
	msgMenuGroup         = "스크립트"
	msgMenuGroupSummary  = "up/update 전후에 실행할 스크립트."
	msgMenuCustom        = "커스텀"
	msgMenuCustomSummary = "이미 있는 스크립트를 경로로 등록."
	msgUsage             = "dcraft hooks [--list|--add|--remove] [--stage pre|post] [--script 경로]\n\n" +
		"등록한 스크립트만 up/update 전후에 실행. 파일명만으로는 실행하지 않음.\n" +
		"처음 실행하거나 내용이 바뀌면 별도의 스크립트 신뢰 승인이 필요.\n" +
		"스크립트는 사용자 권한으로 프로젝트 폴더에서 실행되며 dcraft는 샌드박싱하거나 자동으로 되돌리지 않음."

	msgFlagList   = "등록된 스크립트와 신뢰 상태를 표시 (기본)."
	msgFlagAdd    = "스크립트 등록."
	msgFlagRemove = "등록된 스크립트 제거."
	msgFlagStage  = "실행 시점: pre 또는 post (up/update 전후)"
	msgFlagScript = "프로젝트 기준 상대 경로 또는 절대 로컬 경로"

	msgStageRegister = "등록 저장"
	msgStageRemove   = "등록 제거"
	msgStageList     = "등록 확인"

	msgActionConflict  = "한 번에 하나의 동작만 지정: --list, --add, --remove"
	msgMissingInput    = "필요한 입력이 없음: %s"
	msgUnknownStage    = "지원하지 않는 실행 시점: %s (pre 또는 post)"
	msgNoService       = "스크립트 등록 연결이 준비되지 않았음."
	msgNoServiceAction = "dcraft를 다시 빌드하거나 설치한 뒤 실행."
	msgInputActionOne  = "dcraft hooks --help로 입력 형식 확인."
	msgInputActionTwo  = "경로와 실행 시점(pre/post)을 확인."

	msgAdded        = "스크립트 등록. 등록과 신뢰 승인은 별개이며 다음 up/update에서 경로·시점·내용 식별자를 확인한 뒤 승인 필요."
	msgRemoved      = "스크립트 등록 제거."
	msgNoHooks      = "등록된 호스트 스크립트가 없음. up/update는 스크립트를 실행하지 않음."
	msgTrustPending = "아직 승인되지 않았거나 내용이 바뀐 스크립트가 있음. up/update 전에 별도 스크립트 신뢰 승인이 필요."
	msgDisclosure   = "호스트 스크립트는 up/update에서만 실행됨. 사용자 권한으로 프로젝트 폴더에서 실행되며 dcraft는 샌드박싱하거나 자동으로 되돌리지 않음."

	msgWizardTitle        = "호스트 스크립트 등록"
	msgWizardActionTitle  = "동작 선택"
	msgWizardActionPrompt = "등록할지 제거할지 선택."
	msgWizardAddLabel     = "스크립트 등록"
	msgWizardRemoveLabel  = "등록 제거"
	msgWizardStageTitle   = "실행 시점 선택"
	msgWizardStagePrompt  = "up/update의 어느 시점에 실행할지 선택."
	msgWizardPreLabel     = "사전 (본 작업 전)"
	msgWizardPostLabel    = "사후 (본 작업 완료 후)"
	msgWizardScriptTitle  = "스크립트 경로 입력"
	msgWizardScriptPrompt = "프로젝트 기준 상대 경로 또는 절대 로컬 경로 입력."
	msgWizardScriptHint   = "한 줄에 하나 · 예: scripts/pre-deploy.sh · hooks/migrate.sh · ./scripts/check-db.sh"
	msgWizardPreviewTitle = "등록 적용 확인"

	msgWizardWorkdirPrefix = "실행 위치: "
	msgWizardWorkdir       = "프로젝트 폴더"
	msgWizardMissing       = "스크립트 파일 찾지 못함: %s"
	msgWizardDirectory     = "스크립트 경로가 폴더: %s"
	msgWizardNotExecutable = "실행 권한이 없음. 등록할 수는 있지만 실행 전에 chmod +x가 필요."
	msgWizardNotRegistered = "등록된 스크립트 찾지 못함: %s (%s)"
	msgPartialScripts      = "%d개 저장한 뒤 실패: %s. 저장된 항목은 그대로 남음."

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
