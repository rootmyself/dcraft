package secretref

// Korean user-facing messages owned by secret reference configuration.
const (
	msgConfigUnreadable     = "비밀값 참조 설정을 읽을 수 없습니다."
	msgConfigMalformed      = "비밀값 참조 설정의 형식이 올바르지 않습니다."
	msgConfigRoot           = "비밀값 참조 설정의 최상위는 항목 묶음이어야 합니다."
	msgSecretsNotList       = "secrets 항목은 참조 목록이어야 합니다."
	msgSecretNotMapping     = "비밀값 참조 항목은 항목 묶음이어야 합니다."
	msgSecretFieldKey       = "비밀값 참조의 항목 이름은 문자열이어야 합니다."
	msgSecretUnknownField   = "비밀값 참조에 알 수 없는 항목이 있습니다: %s"
	msgSecretDuplicateField = "비밀값 참조의 항목이 중복되었습니다: %s"
	msgSecretFieldString    = "비밀값 참조의 %s 값은 문자열이어야 합니다."
	msgSecretFieldMissing   = "비밀값 참조에 필요한 항목이 없습니다: %s"
	msgDuplicateTarget      = "서비스 '%s'의 %s 전달 대상 '%s'가 중복되었습니다."
	msgUnknownService       = "비밀값 참조의 서비스가 Compose 설정에 없습니다: %s"
	msgAvailableServices    = "사용 가능한 서비스: "
	msgRemoveNotFound       = "해당 비밀값 참조를 찾지 못했습니다: 서비스 %s, %s, 대상 %s"
	msgInputInvalid         = "비밀값 참조 입력이 올바르지 않습니다."
	msgConfigEncode         = "비밀값 참조 설정을 저장할 형식으로 만들지 못했습니다."
	msgConfigWrite          = "비밀값 참조 설정을 저장하지 못했습니다."
)

var (
	msgConfigActionOne         = ".dcraft.yaml의 secrets 항목을 확인하세요."
	msgConfigActionTwo         = "phase·delivery·source 값과 서비스·대상 이름이 올바른지 확인하세요."
	msgDuplicateAction         = "같은 서비스와 전달 방식에서 대상 이름을 하나만 사용하세요."
	msgUnknownServiceActionOne = "dcraft status로 현재 Compose 서비스 이름을 확인하세요."
	msgUnknownServiceActionTwo = "비밀값 참조의 service를 Compose에 있는 서비스 이름으로 지정하세요."
	msgInputActionOne          = "--service, --delivery, --source, --ref에 필요한 값을 지정하세요."
	msgInputActionTwo          = "사용법: dcraft secrets --add -s <서비스> --delivery environment|file --source env|file --ref <참조>"
	msgListAction              = "dcraft secrets로 현재 참조 목록을 확인하세요."
)

const (
	msgRedacted = "[redacted]"

	msgEnvSourceMissing     = "환경변수 원본을 찾을 수 없습니다: %s"
	msgEnvSourceInvalid     = "환경변수 원본 값을 사용할 수 없습니다: %s"
	msgFileSourceMissing    = "비밀값 원본 파일을 찾을 수 없습니다: %s"
	msgFileSourceUnreadable = "비밀값 원본 파일을 읽을 수 없습니다: %s"
	msgFileSourceInvalid    = "비밀값 원본이 올바른 파일이 아닙니다: %s"
	msgFileSourceEmpty      = "비밀값 원본 파일이 비어 있습니다: %s"

	msgAppliedEnv         = "환경변수로 전달한 비밀값: %s"
	msgAppliedFile        = "파일로 전달한 비밀값: %s (컨테이너의 /run/secrets 아래 경로)"
	msgEnvVisible         = "환경변수 전달 값은 Docker 조회 권한을 가진 사용자가 컨테이너 환경에서 읽을 수 있습니다. dcraft는 값을 출력하거나 저장하지 않습니다."
	msgFileAppSupport     = "파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 동작합니다. dcraft가 앱의 파일 읽기를 보장하지는 않습니다."
	msgFilesEnsured       = "재시작에 필요한 비밀값 파일 전달 상태를 확인했습니다."
	msgStateInsideProject = "상태 폴더가 프로젝트 폴더 안에 있어 비밀값 파일을 안전하게 보관할 수 없습니다."

	msgApplyActionOne      = "Docker 상태와 상태 폴더(DCRAFT_STATE_DIR) 권한·여유 공간을 확인하세요."
	msgApplyActionTwo      = "해결한 뒤 dcraft up을 다시 실행하세요."
	msgEnvSourceActionOne  = "원본 환경변수가 설정된 셸에서 실행하거나, 참조 이름을 확인하세요."
	msgEnvSourceActionTwo  = "dcraft는 빈 값이나 없는 환경변수를 비밀값으로 전달하지 않습니다."
	msgFileSourceActionOne = "원본 파일 경로가 프로젝트 폴더 기준으로 실제 존재하는지 확인하세요."
	msgFileSourceActionTwo = "읽기 권한이 있는 비어 있지 않은 일반 파일을 지정하세요."
)

const (
	msgAppliedOverride        = "dcraft 관리 Compose override: %s (사용자 Compose 파일은 수정하지 않았습니다.)"
	msgPartialOverwrite       = "비밀값 전달 준비가 실패했지만 이미 존재하던 파일 일부가 새 값으로 갱신되었을 수 있습니다. dcraft는 이전 값을 저장하지 않아 되돌리지 않습니다."
	msgPartialOverwriteAction = "dcraft status로 현재 상태를 확인하고, 필요한 경우 dcraft up을 다시 실행하세요."
)
