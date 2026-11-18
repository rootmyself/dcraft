package initconfig

// Korean user-facing text owned by the first-run configuration feature.
// Title, Summary and Usage are the Korean registration text consumed by the
// thin feature package.
func Title() string   { return msgTitle }
func Summary() string { return msgSummary }
func Usage() string   { return msgUsage }

const (
	msgTitle   = "최초 구성"
	msgSummary = "Dockerfile 또는 직접 입력한 이미지로 일반 Compose 파일을 만듭니다."
	msgUsage   = `dcraft init --from dockerfile|image [옵션]

현재 폴더에 Compose 설정이 없을 때 필요한 값만 받아 compose.yaml을 만듭니다.
생성 내용과 저장 경로를 먼저 보여 주고, 저장은 별도 확인 뒤에만 합니다.
저장 후 dcraft가 자동으로 실행하지 않으며 다음 명령을 안내합니다.

Dockerfile 방식:
  dcraft init --from dockerfile --service-name app --context . --dockerfile Dockerfile \
    --port 8080:80 --volume ./data:/data --restart unless-stopped

이미지 방식:
  dcraft init --from image --service-name web --image nginx:1.27-alpine --port 8080:80

기존 Compose 파일이 있으면 덮어쓰지 않습니다. 파일을 직접 확인하거나
--output <기존 파일> --approve file_overwrite를 함께 지정해야 합니다.
Dockerfile의 EXPOSE/CMD는 자동으로 읽지 않습니다. 포트와 명령은 직접 입력한
값만 기록하며, 이미지/Dockerfile에서 포트나 데이터 경로를 추측하지 않습니다.`

	msgStageCollect  = "입력 확인"
	msgStagePreview  = "생성 미리보기"
	msgStageValidate = "Compose 검증"
	msgStageWrite    = "파일 저장"

	msgModeMissing    = "구성 방식을 지정해야 합니다."
	msgModeInvalid    = "구성 방식은 dockerfile 또는 image만 사용할 수 있습니다."
	msgServiceMissing = "서비스 이름을 지정해야 합니다."
	msgServiceInvalid = "서비스 이름에 사용할 수 없는 문자가 있습니다."
	msgImageMissing   = "이미지 방식을 선택했다면 이미지 참조를 지정해야 합니다."
	msgImageInvalid   = "이미지 참조 형식이 올바르지 않습니다."
	msgImageModeOnly  = "이미지 방식에서는 --context와 --dockerfile을 사용하지 않습니다. 이미지 실행에 필요한 값만 지정하세요."
	msgDockerfileMode = "Dockerfile 방식에서는 --image를 사용하지 않습니다. 빌드 결과 이름이 필요하면 Compose 파일을 직접 편집하세요."
	msgPortInvalid    = "포트 형식이 올바르지 않습니다."
	msgVolumeInvalid  = "볼륨 형식이 올바르지 않습니다."
	msgEnvInvalid     = "환경변수 형식이 올바르지 않습니다."
	msgRestartInvalid = "restart 값이 올바르지 않습니다."
	msgOutputInvalid  = "저장 경로가 올바르지 않습니다."
	msgOutputSymlink  = "저장 경로가 심볼릭 링크입니다. 링크 대상이나 다른 파일을 대신 바꾸지 않습니다."
	msgOutputDir      = "저장 경로가 폴더입니다. 파일 경로를 지정하세요."
	msgOutputParent   = "저장 폴더를 찾지 못했습니다. dcraft는 폴더를 새로 만들지 않습니다."
	msgOutputSpecial  = "저장 경로가 일반 파일이 아닙니다."

	msgContextMissing   = "빌드 컨텍스트 폴더를 찾지 못했습니다."
	msgContextNotDir    = "빌드 컨텍스트가 폴더가 아닙니다."
	msgDockerfileMiss   = "Dockerfile을 찾지 못했습니다."
	msgDockerfileNotReg = "Dockerfile 경로가 일반 파일이 아닙니다."

	msgExistingConfig    = "이미 Compose 설정이 있어 새 파일을 만들지 않았습니다."
	msgExistingAction    = "기존 파일을 그대로 사용하려면 dcraft status로 현재 상태를 확인하세요."
	msgExistingOverwrite = "덮어쓰려면 --output <기존 파일>과 --approve file_overwrite를 함께 지정하세요."
	msgExistingUntouched = "dcraft는 기존 Compose 파일과 Dockerfile을 수정하지 않았습니다."

	msgComposeRejected    = "생성한 Compose 내용을 Compose가 거부했습니다. 파일을 저장하지 않았습니다."
	msgComposeRejectedAct = "표시된 Compose 오류의 필드를 수정한 뒤 다시 실행하세요."
	msgComposeNoFile      = "저장된 파일은 없으며 기존 파일도 변경되지 않았습니다."
	msgComposeMissing     = "Docker Compose를 찾지 못했습니다. dcraft init은 생성 내용을 Compose로 검증합니다."
	msgComposeMissingAct  = "Docker Compose를 설치한 뒤 다시 실행하거나, 설치를 승인해 dcraft install-compose를 사용하세요."
	msgComposeOutput      = "Compose 검증 결과를 해석하지 못했습니다. 파일을 저장하지 않았습니다."
	msgComposeServiceMiss = "생성한 서비스가 Compose 검증 결과에 없습니다. 파일을 저장하지 않았습니다."
	msgComposeNameMiss    = "Compose가 프로젝트 이름을 확인하지 못했습니다. 파일을 저장하지 않았습니다."

	msgSaveFailed    = "Compose 파일을 저장하지 못했습니다."
	msgSaveFailedAct = "저장 폴더의 권한과 여유 공간을 확인하세요. 임시 파일은 정리되며 기존 파일은 저장이 완료되기 전까지 그대로입니다."
	msgSaveExists    = "저장 중 다른 파일이 같은 경로에 생겨 덮어쓰지 않았습니다. 파일을 확인한 뒤 다시 실행하세요."

	msgToolMissing = "Docker CLI를 찾지 못했습니다."
	msgToolAction  = "Docker를 설치한 뒤 다시 실행하세요."

	msgNextCommand = "dcraft up"
	msgNoRun       = "저장은 실행이 아닙니다. dcraft는 저장 후 자동으로 up을 실행하지 않았습니다."
	msgRunInDir    = "다음 명령은 프로젝트 폴더에서 실행하세요."

	msgNoticeEnvPlain    = "environment 값은 compose.yaml에 평문으로 저장됩니다. 비밀값은 여기에 입력하지 말고 dcraft secrets 참조를 사용하세요."
	msgNoticeExpose      = "Dockerfile의 EXPOSE/CMD는 자동으로 사용하지 않았습니다. 포트와 명령은 직접 입력한 값만 기록합니다."
	msgNoticeEditable    = "생성된 파일은 일반 Compose 파일입니다. dcraft 없이도 편집하고 실행할 수 있습니다."
	msgNoticeOutside     = "저장 경로가 프로젝트 폴더 밖입니다. 명시한 경로에만 저장하며 다른 폴더는 건드리지 않습니다."
	msgNoticeOverwrite   = "기존 파일을 새 내용으로 덮어씁니다. 이전 내용은 백업되지 않습니다."
	msgNoticeProjectName = "명시한 -p/--project-name 값은 생성 파일에 기록되지 않습니다. dcraft 실행 시 같은 값을 지정하거나 자동 이름 배정을 사용하세요."
	msgNoticeComposeName = "Compose가 폴더와 저장 위치에서 파생한 프로젝트 이름입니다. dcraft 실행 시 충돌하면 가장 작은 번호 접미사가 붙을 수 있습니다."

	msgPreviewTitle      = "Compose 파일 미리보기"
	msgPreviewMode       = "방식"
	msgPreviewService    = "서비스"
	msgPreviewPath       = "저장 경로"
	msgPreviewName       = "프로젝트 이름(Compose 파생)"
	msgPreviewContent    = "생성 내용"
	msgPreviewEnd        = "내용 끝"
	msgPreviewCheck      = "검증"
	msgPreviewCreates    = "생성/변경"
	msgPreviewResources  = "예상 리소스"
	msgPreviewNext       = "다음 명령"
	msgPreviewRef        = "참고"
	msgPreviewApproval   = "필요한 승인"
	msgApprovalOverwrite = "파일 덮어쓰기"
	msgPreviewNoSave     = "아직 저장하지 않았습니다. 저장 여부를 확인하세요."
	msgPreviewInDir      = "프로젝트 폴더에서 실행하세요."

	msgWizardTitle          = "최초 Compose 설정"
	msgWizardModeTitle      = "구성 방식"
	msgWizardModePrompt     = "Dockerfile을 빌드할지, 직접 입력한 이미지를 실행할지 선택하세요. 포트와 데이터 경로는 자동으로 추측하지 않습니다."
	msgWizardModeDocker     = "Dockerfile로 빌드"
	msgWizardModeImage      = "이미지 직접 입력"
	msgWizardServiceTitle   = "서비스 이름"
	msgWizardServicePrompt  = "Compose 서비스 이름을 입력하세요. 기본값 app은 제안일 뿐이며 미리보기에서 확인합니다."
	msgWizardServiceHint    = "예: app, web"
	msgWizardContextTitle   = "빌드 컨텍스트"
	msgWizardContextPrompt  = "Dockerfile 빌드에 보낼 폴더입니다. 상대 경로는 프로젝트 폴더 기준이며 새 폴더를 만들지 않습니다."
	msgWizardContextHint    = "예: . 또는 ./app"
	msgWizardDockerTitle    = "Dockerfile 경로"
	msgWizardDockerPrompt   = "빌드 컨텍스트 기준 Dockerfile 경로입니다. 파일이 실제로 있어야 합니다."
	msgWizardDockerHint     = "예: Dockerfile 또는 docker/Dockerfile"
	msgWizardImageTitle     = "이미지 참조"
	msgWizardImagePrompt    = "실행할 이미지 참조를 입력하세요. 태그나 digest를 포함할 수 있습니다."
	msgWizardImageHint      = "예: nginx:1.27-alpine"
	msgWizardOutputTitle    = "저장 경로"
	msgWizardOutputPrompt   = "파일을 저장할 경로입니다. 상대 경로는 프로젝트 폴더 기준이며 폴더는 새로 만들지 않습니다."
	msgWizardOutputHint     = "예: compose.yaml"
	msgWizardPortTitle      = "포트"
	msgWizardPortPrompt     = "공개할 포트를 입력하세요. 비우면 포트를 추가하지 않습니다."
	msgWizardPortHint       = "예: 8080:80, 127.0.0.1:8080:80 (여러 개는 쉼표)"
	msgWizardVolumeTitle    = "볼륨"
	msgWizardVolumePrompt   = "마운트를 입력하세요. 비우면 볼륨을 추가하지 않습니다."
	msgWizardVolumeHint     = "예: ./data:/data, cache:/cache:ro (여러 개는 쉼표)"
	msgWizardEnvTitle       = "환경변수"
	msgWizardEnvPrompt      = "KEY=VALUE를 입력하세요. 값은 compose.yaml에 평문으로 저장되며 비밀값은 입력하지 마세요."
	msgWizardEnvHint        = "예: APP_ENV=dev, LOG_LEVEL=info (여러 개는 쉼표)"
	msgWizardCommandTitle   = "명령"
	msgWizardCommandPrompt  = "이미지의 기본 CMD를 덮어쓸 명령입니다. 비우면 이미지 기본값을 사용합니다."
	msgWizardCommandHint    = "예: httpd -f -p 8080"
	msgWizardRestartTitle   = "재시작 정책"
	msgWizardRestartPrompt  = "컨테이너 재시작 정책을 선택하세요. 지정하지 않으면 Compose 기본값을 사용합니다."
	msgWizardRestartNone    = "지정 안 함 (Compose 기본값)"
	msgWizardRestartNo      = "no"
	msgWizardRestartAlways  = "always"
	msgWizardRestartUnless  = "unless-stopped"
	msgWizardRestartFailure = "on-failure"

	msgNoticeOtherExisting = "기존 Compose 파일이 여러 개 있습니다. 이번에는 선택한 파일만 바꾸며 나머지는 그대로 둡니다."
	msgNoticeOverride      = "기존 override 파일이 있습니다: %s. dcraft는 수정하지 않으며, 생성 후 Compose가 이 파일을 자동으로 병합합니다."

	msgSaved        = "Compose 파일을 저장했습니다."
	msgSavedPath    = "저장 경로"
	msgSavedService = "서비스"
	msgSavedCheck   = "검증"
	msgSavedChanged = "변경"
	msgSavedNew     = "새 파일을 만들었습니다."
	msgSavedReplace = "기존 파일을 덮어썼습니다."
	msgSavedNoOther = "다른 사용자 파일과 Dockerfile은 수정하지 않았습니다."

	msgCanceled    = "최초 구성이 중단되었습니다. 저장 단계가 시작된 뒤라면 대상 파일이 이미 바뀌었을 수 있으므로 내용을 확인하세요. 임시 파일은 남지 않습니다."
	msgCanceledAct = "대상 파일 내용을 확인하고 필요하면 같은 명령을 다시 실행하세요."
)
