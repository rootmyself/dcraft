package environments

// Korean user-facing messages owned by environment config loading.
const (
	msgConfigUnreadable      = "실행환경 설정 파일을 읽을 수 없음."
	msgConfigMalformed       = "실행환경 설정 파일의 형식이 올바르지 않음."
	msgConfigRoot            = "실행환경 설정 파일의 최상위는 항목 묶음이어야 함."
	msgConfigEnvironments    = "environments 항목은 별칭 묶음이어야 함."
	msgConfigAliasKey        = "실행환경 별칭은 문자열이어야 함."
	msgConfigAliasInvalid    = "실행환경 별칭이 올바르지 않음: %s"
	msgConfigAliasDuplicate  = "실행환경 별칭이 중복되었음: %s"
	msgEnvNotMapping         = "실행환경 '%s'의 정의는 항목 묶음이어야 함."
	msgEnvEmpty              = "실행환경 '%s'에 Compose 파일·환경 파일·프로필이 모두 없음."
	msgEnvUnknownField       = "실행환경 '%s'에 알 수 없는 항목이 있음: %s"
	msgEnvDuplicateField     = "실행환경 '%s'의 항목이 중복되었음: %s"
	msgEnvNotList            = "실행환경 '%s'의 %s은 목록이어야 함."
	msgEnvListItem           = "실행환경 '%s'의 %s에는 문자열만 넣을 수 있음."
	msgEnvListEmpty          = "실행환경 '%s'의 %s에 빈 값이 있음."
	msgEnvListDuplicate      = "실행환경 '%s'의 %s에 같은 값이 중복되었음: %s"
	msgEnvProfileInvalid     = "실행환경 '%s'의 프로필 이름이 올바르지 않음: %s"
	msgEnvComposeFileMissing = "실행환경 '%s'가 참조하는 Compose 파일을 찾을 수 없음."
	msgEnvEnvFileMissing     = "실행환경 '%s'가 참조하는 환경 파일을 찾을 수 없음."
	msgUnknownEnv            = "등록되지 않은 실행환경: %s. 사용 가능한 실행환경: %s"
	msgUnknownEnvNone        = "이 폴더에는 이름 붙인 실행환경이 없음: %s"
)

var (
	msgConfigActionOne     = ".dcraft.yaml의 environments 항목 확인."
	msgConfigActionTwo     = "실행환경 별칭은 소문자·숫자·하이픈·밑줄로 32자 이내여야 함."
	msgEnvFileAction       = "설정에 적은 경로가 프로젝트 폴더 기준으로 실제 존재하는 파일인지 확인."
	msgUnknownEnvActionOne = "--env에 사용 가능한 실행환경 별칭 지정."
	msgUnknownEnvActionTwo = "실행환경을 등록하려면 dcraft environments --add --name <별칭>을 실행."
)
