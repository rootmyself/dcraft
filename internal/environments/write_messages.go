package environments

// Korean user-facing text owned by environment-alias creation and removal.
// Command names and machine values stay English.
const (
	msgWriteNameMissing     = "실행환경 별칭이 필요."
	msgWriteAliasInvalid    = "실행환경 별칭이 올바르지 않음: %s"
	msgWriteDuplicateAlias  = "이미 등록된 실행환경 별칭: %s"
	msgWriteFileMissing     = "%s을 찾을 수 없음: %s"
	msgWriteFileUnreadable  = "%s을 읽을 수 없음: %s"
	msgWriteFileNotRegular  = "%s 경로가 일반 파일이 아님: %s"
	msgWriteListEmpty       = "%s 목록에 빈 값이 있음."
	msgWriteListDuplicate   = "%s 목록에 같은 값이 중복되었음: %s"
	msgWriteProfileSpace    = "프로필 이름에는 공백을 넣을 수 없음: %s"
	msgWriteNoInputs        = "실행환경 '%s'에 Compose 파일·환경 파일·프로필이 모두 없음."
	msgWriteRemoveUnknown   = "등록되지 않은 실행환경: %s"
	msgWriteFailed          = "실행환경 설정 파일 저장 실패."
	msgConfigEncode         = "실행환경 설정을 YAML로 만들지 못함."
	msgWriteLockFailed      = "실행환경 설정 잠금 처리 실패."
	msgWriteLocked          = "다른 dcraft 명령이 실행환경 설정을 수정 중."
	msgWriteComposeLabel    = "Compose 파일"
	msgWriteEnvFileLabel    = "환경 파일"
	msgWriteProfileLabel    = "프로필"
	msgWriteRemoveActionOne = "dcraft environments --list로 등록된 별칭 확인."
	msgWriteRemoveActionTwo = "별칭 이름은 소문자·숫자·하이픈·밑줄로 32자 이내여야 함."
	msgWriteLockActionOne   = "다른 dcraft 명령이 끝난 뒤 다시 실행."
	msgWriteLockActionTwo   = "프로젝트 폴더의 .dcraft.lock 권한 확인."
)
