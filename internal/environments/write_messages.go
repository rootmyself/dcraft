package environments

// Korean user-facing text owned by environment-alias creation and removal.
// Command names and machine values stay English.
const (
	msgWriteNameMissing     = "실행환경 별칭이 필요합니다."
	msgWriteAliasInvalid    = "실행환경 별칭이 올바르지 않습니다: %s"
	msgWriteDuplicateAlias  = "이미 등록된 실행환경 별칭입니다: %s"
	msgWriteFileMissing     = "%s을 찾을 수 없습니다: %s"
	msgWriteFileUnreadable  = "%s을 읽을 수 없습니다: %s"
	msgWriteFileNotRegular  = "%s 경로가 일반 파일이 아닙니다: %s"
	msgWriteListEmpty       = "%s 목록에 빈 값이 있습니다."
	msgWriteListDuplicate   = "%s 목록에 같은 값이 중복되었습니다: %s"
	msgWriteProfileSpace    = "프로필 이름에는 공백을 넣을 수 없습니다: %s"
	msgWriteNoInputs        = "실행환경 '%s'에 Compose 파일·환경 파일·프로필이 모두 없습니다."
	msgWriteRemoveUnknown   = "등록되지 않은 실행환경입니다: %s"
	msgWriteFailed          = "실행환경 설정 파일을 저장하지 못했습니다."
	msgConfigEncode         = "실행환경 설정을 YAML로 만들지 못했습니다."
	msgWriteLockFailed      = "실행환경 설정 잠금을 처리하지 못했습니다."
	msgWriteLocked          = "다른 dcraft 명령이 실행환경 설정을 수정 중입니다."
	msgWriteComposeLabel    = "Compose 파일"
	msgWriteEnvFileLabel    = "환경 파일"
	msgWriteProfileLabel    = "프로필"
	msgWriteRemoveActionOne = "dcraft environments --list로 등록된 별칭을 확인하세요."
	msgWriteRemoveActionTwo = "별칭 이름은 소문자·숫자·하이픈·밑줄로 32자 이내여야 합니다."
	msgWriteLockActionOne   = "다른 dcraft 명령이 끝난 뒤 다시 실행하세요."
	msgWriteLockActionTwo   = "프로젝트 폴더의 .dcraft.lock 권한을 확인하세요."
)
