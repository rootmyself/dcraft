package secrets

// Korean user-facing text owned by the secrets feature. Command names, flags,
// phase/delivery/source machine values and JSON keys stay English.
const (
	msgTitle   = "비밀값 연결"
	msgSummary = "비밀값 원본 참조를 저장·조회·제거합니다."
	msgUsage   = `dcraft secrets [--add | --remove | --list] [--json]
  dcraft secrets --add -s <서비스> --delivery environment|file --source env|file --ref <참조> [--phase runtime] [--target <이름>]
  dcraft secrets --remove -s <서비스> --delivery environment|file --target <이름>
  dcraft secrets

저장되는 것은 원본 위치와 전달 방식뿐이며 해석된 비밀값은 저장하지 않습니다.
환경변수 전달 값은 Docker 조회 권한이 있는 사용자가 컨테이너 환경에서 읽을 수 있습니다.
파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 동작합니다.
대화형 화면에서는 서비스 → 시점 → 전달 방식 → 원본 참조 순서로 선택합니다.`

	msgFlagAdd      = "비밀값 참조를 추가합니다."
	msgFlagRemove   = "비밀값 참조를 제거합니다."
	msgFlagList     = "비밀값 참조를 조회합니다 (기본 동작)."
	msgFlagPhase    = "전달 시점 (runtime만 지원)"
	msgFlagDelivery = "전달 방식 (environment 또는 file)"
	msgFlagSource   = "원본 종류 (env 또는 file)"
	msgFlagRef      = "환경변수 이름 또는 프로젝트 기준 로컬 파일 경로"
	msgFlagTarget   = "전달 대상 이름 (환경변수 이름 또는 /run/secrets 파일 이름)"

	msgNoCompose        = "Compose 서비스를 확인할 수 없어 비밀값 참조를 저장하지 않았습니다."
	msgNoComposeAction  = "Compose 설정과 Docker 상태를 확인한 뒤 다시 실행하세요."
	msgNoServices       = "Compose 설정에 서비스가 없습니다."
	msgNoServicesAction = "Compose 설정에 서비스를 추가한 뒤 다시 실행하세요."

	msgActionConflict    = "한 번에 하나의 동작만 지정하세요: --add, --remove, --list"
	msgOneService        = "서비스 하나를 -s/--service로 지정하세요."
	msgMissingInput      = "필요한 입력이 없습니다: %s"
	msgRemoveNeeds       = "참조를 제거하려면 --delivery와 --target을 지정하세요."
	msgRemoveMissing     = "해당 비밀값 참조를 찾지 못했습니다: 서비스 %s, %s, 대상 %s"
	msgAdded             = "비밀값 참조를 저장했습니다."
	msgRemoved           = "비밀값 참조를 제거했습니다."
	msgRemovedFileNotice = "파일 전달 값은 다음 성공적인 dcraft up(서비스 재생성) 또는 dcraft down까지 상태 폴더에 남습니다. 그래야 실행 중인 컨테이너가 그 사이에도 재시작될 수 있습니다."
	msgOverrideFailed    = "참조는 저장했지만 dcraft 관리 Compose override를 갱신하지 못했습니다."
	msgOverrideAction    = "다음 dcraft up에서 override를 다시 만들 수 있도록 상태 폴더 권한을 확인하세요."

	msgInputActionOne = "dcraft secrets --add -s <서비스> --delivery environment|file --source env|file --ref <참조>"
	msgInputActionTwo = "dcraft secrets로 현재 참조 목록을 확인하세요."

	msgWizardTitle          = "비밀값 연결 설정"
	msgWizardServiceTitle   = "서비스 선택"
	msgWizardServicePrompt  = "비밀값을 전달할 Compose 서비스를 선택하세요."
	msgWizardPhaseTitle     = "전달 시점 선택"
	msgWizardPhasePrompt    = "현재는 실행 시점(runtime)만 지원합니다. 빌드 시점은 별도 기능에서 제공됩니다."
	msgWizardPhaseRuntime   = "실행 시점 (runtime)"
	msgWizardDeliveryTitle  = "전달 방식 선택"
	msgWizardDeliveryPrompt = "앱이 읽을 방식을 선택하세요. 환경변수는 Docker 권한자에게 보일 수 있고, 파일은 앱이 직접 읽어야 합니다."
	msgWizardDeliveryEnv    = "환경변수 (environment)"
	msgWizardDeliveryFile   = "서비스 파일 /run/secrets (file)"
	msgWizardSourceTitle    = "원본 종류 선택"
	msgWizardSourcePrompt   = "값을 읽어올 원본을 선택하세요."
	msgWizardSourceEnv      = "환경변수 (env)"
	msgWizardSourceFile     = "로컬 파일 (file)"
	msgWizardRefTitle       = "원본 참조 입력"
	msgWizardRefPrompt      = "원본 참조를 입력하세요."
	msgWizardRefHint        = "환경변수 이름 또는 프로젝트 폴더 기준 파일 경로 (예: DB_PASSWORD, secrets/token.txt)"

	msgPreviewTitle      = "저장할 비밀값 연결"
	msgPreviewService    = "서비스"
	msgPreviewPhase      = "시점"
	msgPreviewDelivery   = "전달"
	msgPreviewSource     = "원본"
	msgPreviewEffect     = "효과"
	msgPreviewInvalid    = "아직 저장할 수 없습니다:"
	msgPreviewNote       = "dcraft는 해석된 값을 .dcraft.yaml이나 이력에 저장하지 않습니다."
	msgPreviewEnvVisible = "환경변수 전달 값은 Docker 조회 권한을 가진 사용자가 컨테이너 환경에서 읽을 수 있습니다."
	msgPreviewFileApp    = "파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 동작합니다."

	msgPhaseRuntime      = "실행 시점 (runtime)"
	msgDeliveryEnvLabel  = "환경변수 %s"
	msgDeliveryFileLabel = "파일 %s"
	msgSourceEnv         = "환경변수"
	msgSourceFile        = "로컬 파일"

	msgEffectEnv     = "dcraft up/restart 시 환경변수 %s 값으로 전달합니다."
	msgEffectFile    = "dcraft up/restart 시 파일 %s 경로로 전달합니다."
	msgEffectUnknown = "전달 방식을 확인할 수 없습니다."

	msgEnvVisible = "환경변수 전달 값은 Docker 조회 권한이 있는 사용자가 컨테이너 환경에서 읽을 수 있습니다."
	msgFileApp    = "파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 하며, dcraft는 앱의 파일 소비를 보장하지 않습니다."

	msgListTitle      = "비밀값 연결"
	msgListEmpty      = "저장된 비밀값 참조가 없습니다."
	msgListConfig     = "설정 파일"
	msgListOverride   = "dcraft 관리 Compose override"
	msgListSource     = "원본"
	msgListEffect     = "효과"
	msgListNotice     = "알림"
	msgListAddHint    = "추가: dcraft secrets --add -s <서비스> --delivery environment|file --source env|file --ref <참조>"
	msgListTUIHint    = "대화형 화면에서는 안내에 따라 선택할 수 있습니다."
	msgMigrationGuide = "기존 Compose 파일이나 .env에 직접 적은 민감값은 dcraft가 자동으로 찾아 제거하지 않습니다. 원본을 참조로 옮기려면 dcraft secrets를 사용하세요. Docker 자체의 저장·조회 경계는 dcraft가 통제하지 않습니다."
)

const (
	msgStageValidate   = "입력 확인"
	msgStageSave       = "참조 저장"
	msgStageRemove     = "참조 제거"
	msgStageList       = "참조 조회"
	msgStageRegenerate = "전달 설정 갱신"
	msgStageWizard     = "설정 단계 준비"
)
