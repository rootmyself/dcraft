package target

// Korean user-facing messages owned by target resolution.
const (
	msgWorkingDirUnknown          = "현재 작업 폴더를 확인할 수 없습니다."
	msgWorkingDirAction           = "터미널을 다시 열거나 --path로 대상 폴더를 지정하세요."
	msgTargetMissing              = "대상 폴더를 찾을 수 없습니다."
	msgTargetMissingAction        = "--path 값이 실제로 존재하는 폴더인지 확인하세요."
	msgTargetUnreadable           = "대상 폴더를 읽을 수 없습니다."
	msgTargetUnreadableAction     = "폴더 접근 권한을 확인하세요."
	msgTargetNotDirectory         = "대상 경로가 폴더가 아닙니다."
	msgTargetNotDirectoryAction   = "--path에는 Compose 파일이 있는 폴더를 지정하세요."
	msgNoConfiguration            = "대상 폴더에서 Compose 설정을 찾지 못했습니다."
	msgNoConfigurationActionOne   = "compose.yaml 또는 docker-compose.yml을 대상 폴더에 추가하세요."
	msgNoConfigurationActionTwo   = "다른 폴더를 대상으로 하려면 --path로 지정하세요."
	msgNoConfigurationActionThree = "dcraft는 상위 폴더를 자동으로 탐색하지 않습니다."
	msgComposeFileMissing         = "지정한 Compose 파일을 찾을 수 없습니다."
	msgEnvFileMissing             = "지정한 환경 파일을 찾을 수 없습니다."
	msgExplicitUnreadable         = "지정한 파일을 읽을 수 없습니다."
	msgExplicitNotFile            = "지정한 경로가 일반 파일이 아닙니다."
	msgMultipleConfigFiles        = "대상 폴더에 여러 개의 Compose 파일이 있어 첫 번째 파일을 사용합니다."
	msgMultipleConfigFilesAction  = "사용할 파일을 직접 선택하려면 -f로 파일을 지정하세요."
)

var (
	msgComposeFileActions = []string{"-f로 지정한 파일 경로를 확인하세요. 상대 경로는 현재 작업 폴더를 기준으로 합니다."}
	msgEnvFileActions     = []string{"--env-file로 지정한 파일 경로를 확인하세요. 상대 경로는 현재 작업 폴더를 기준으로 합니다."}
)
