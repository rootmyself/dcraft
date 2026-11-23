package target

// Korean user-facing messages owned by target resolution.
const (
	msgWorkingDirUnknown          = "현재 작업 폴더 확인 불가."
	msgWorkingDirAction           = "터미널을 다시 열거나 --path로 대상 폴더 지정."
	msgTargetMissing              = "대상 폴더를 찾을 수 없음."
	msgTargetMissingAction        = "--path 값이 실제로 존재하는 폴더인지 확인."
	msgTargetUnreadable           = "대상 폴더를 읽을 수 없음."
	msgTargetUnreadableAction     = "폴더 접근 권한 확인."
	msgTargetNotDirectory         = "대상 경로가 폴더가 아님."
	msgTargetNotDirectoryAction   = "--path에는 Compose 파일이 있는 폴더 지정."
	msgNoConfiguration            = "대상 폴더에서 Compose 설정 찾지 못함."
	msgNoConfigurationActionOne   = "dcraft init으로 Dockerfile 또는 이미지에서 compose.yaml을 만들거나, compose.yaml 또는 docker-compose.yml을 대상 폴더에 추가."
	msgNoConfigurationActionTwo   = "다른 폴더를 대상으로 하려면 --path로 지정."
	msgNoConfigurationActionThree = "dcraft는 상위 폴더를 자동으로 탐색하지 않음."
	msgComposeFileMissing         = "지정한 Compose 파일을 찾을 수 없음."
	msgEnvFileMissing             = "지정한 환경 파일을 찾을 수 없음."
	msgExplicitUnreadable         = "지정한 파일을 읽을 수 없음."
	msgExplicitNotFile            = "지정한 경로가 일반 파일이 아님."
	msgMultipleConfigFiles        = "대상 폴더에 여러 개의 Compose 파일이 있어 첫 번째 파일 사용."
	msgMultipleConfigFilesAction  = "사용할 파일을 직접 선택하려면 -f로 파일 지정."
	msgEnvironmentRequired        = "실행환경 선택 필요. 사용 가능한 실행환경: %s"
	msgEnvironmentInputConflict   = "실행환경을 선택했을 때는 -f, --env-file, --profile을 함께 사용 불가."
	msgEnvironmentUnknownNone     = "이 폴더에는 이름 붙인 실행환경이 없음: %s"
	msgEnvironmentLimits          = "실행환경은 Compose 입력과 dcraft 상태·이력을 구분하지만, 외부 볼륨·bind mount·네트워크·고정 호스트 포트는 격리하지 않음."
	msgProjectNameShared          = "실행환경 %s가 같은 Compose 프로젝트 이름 '%s'을 사용."
	msgEnvironmentOverlap         = "실행환경 '%s'와 '%s'가 같은 호스트 자원 사용."
)

var (
	msgComposeFileActions                = []string{"-f로 지정한 파일 경로 확인. 상대 경로는 현재 작업 폴더 기준."}
	msgEnvFileActions                    = []string{"--env-file로 지정한 파일 경로 확인. 상대 경로는 현재 작업 폴더 기준."}
	msgEnvironmentRequiredActionOne      = "--env <별칭>으로 실행환경을 명시적으로 지정."
	msgEnvironmentRequiredActionTwo      = "대화형 화면에서는 실행환경을 직접 선택해야 하며, 첫 항목이나 prod를 자동으로 고르지 않음."
	msgEnvironmentRequiredActionThree    = "실행환경 목록은 프로젝트 폴더의 .dcraft.yaml에서 확인 가능."
	msgEnvironmentInputConflictActionOne = "선택한 실행환경의 Compose 파일·환경 파일·프로필은 .dcraft.yaml에서 수정."
	msgEnvironmentInputConflictActionTwo = "직접 파일을 지정하려면 --env를 제거."
	msgEnvironmentUnknownNoneActionOne   = "--env를 제거하거나 프로젝트 폴더의 .dcraft.yaml에 실행환경 등록."
	msgEnvironmentUnknownNoneActionTwo   = "실행환경 별칭은 소문자·숫자·하이픈·밑줄로 32자 이내여야 함."
	msgEnvironmentLimitsAction           = "환경 간에 같은 호스트 포트·외부 볼륨·절대 경로 bind mount를 쓰는지 Compose 선언 확인."
	msgProjectNameSharedActionOne        = "실행환경마다 다른 프로젝트 이름을 쓰려면 -p를 제거하고 Compose 프로젝트 이름을 환경별로 지정."
	msgProjectNameSharedActionTwo        = "dcraft는 명시한 프로젝트 이름을 임의로 바꾸지 않음. 같은 이름이면 실제 컨테이너·네트워크를 공유."
	msgEnvironmentOverlapActionOne       = "고정 호스트 포트는 한 번에 하나의 스택만 사용 가능."
	msgEnvironmentOverlapActionTwo       = "외부 볼륨·bind mount·네트워크는 dcraft가 격리하지 않음. 의도한 공유인지 확인."
)
