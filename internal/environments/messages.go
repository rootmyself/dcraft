package environments

// Korean user-facing messages owned by environment config loading.
const (
	msgConfigUnreadable      = "실행환경 설정 파일을 읽을 수 없습니다."
	msgConfigMalformed       = "실행환경 설정 파일의 형식이 올바르지 않습니다."
	msgConfigRoot            = "실행환경 설정 파일의 최상위는 항목 묶음이어야 합니다."
	msgConfigEnvironments    = "environments 항목은 별칭 묶음이어야 합니다."
	msgConfigAliasKey        = "실행환경 별칭은 문자열이어야 합니다."
	msgConfigAliasInvalid    = "실행환경 별칭이 올바르지 않습니다: %s"
	msgConfigAliasDuplicate  = "실행환경 별칭이 중복되었습니다: %s"
	msgEnvNotMapping         = "실행환경 '%s'의 정의는 항목 묶음이어야 합니다."
	msgEnvEmpty              = "실행환경 '%s'에 Compose 파일·환경 파일·프로필이 모두 없습니다."
	msgEnvUnknownField       = "실행환경 '%s'에 알 수 없는 항목이 있습니다: %s"
	msgEnvDuplicateField     = "실행환경 '%s'의 항목이 중복되었습니다: %s"
	msgEnvNotList            = "실행환경 '%s'의 %s은 목록이어야 합니다."
	msgEnvListItem           = "실행환경 '%s'의 %s에는 문자열만 넣을 수 있습니다."
	msgEnvListEmpty          = "실행환경 '%s'의 %s에 빈 값이 있습니다."
	msgEnvListDuplicate      = "실행환경 '%s'의 %s에 같은 값이 중복되었습니다: %s"
	msgEnvProfileInvalid     = "실행환경 '%s'의 프로필 이름이 올바르지 않습니다: %s"
	msgEnvComposeFileMissing = "실행환경 '%s'가 참조하는 Compose 파일을 찾을 수 없습니다."
	msgEnvEnvFileMissing     = "실행환경 '%s'가 참조하는 환경 파일을 찾을 수 없습니다."
	msgUnknownEnv            = "등록되지 않은 실행환경입니다: %s. 사용 가능한 실행환경: %s"
	msgUnknownEnvNone        = "이 폴더에는 이름 붙인 실행환경이 없습니다: %s"
)

var (
	msgConfigActionOne     = ".dcraft.yaml의 environments 항목을 확인하세요."
	msgConfigActionTwo     = "실행환경 별칭은 소문자·숫자·하이픈·밑줄로 32자 이내여야 합니다."
	msgEnvFileAction       = "설정에 적은 경로가 프로젝트 폴더 기준으로 실제 존재하는 파일인지 확인하세요."
	msgUnknownEnvActionOne = "--env에 사용 가능한 실행환경 별칭을 지정하세요."
	msgUnknownEnvActionTwo = "실행환경을 등록하려면 dcraft environments --add --name <별칭>을 실행하세요."
)
