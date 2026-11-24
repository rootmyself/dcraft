package initconfig

// Korean user-facing text owned by the first-run configuration feature.
// Title, Summary and Usage are the Korean registration text consumed by the
// thin feature package.
func Title() string   { return msgTitle }
func Summary() string { return msgSummary }
func Usage() string   { return msgUsage }

const (
	msgTitle   = "최초 구성"
	msgSummary = "Dockerfile 또는 직접 입력한 이미지로 일반 Compose 파일을 만듦."
	msgUsage   = `dcraft init --from dockerfile|image [옵션]

현재 폴더에 Compose 설정이 없을 때 필요한 값만 받아 compose.yaml을 만듦.
생성 내용과 저장 경로를 먼저 보여 주고, 저장은 별도 확인 뒤에만 실행.
저장 후 dcraft가 자동으로 실행하지 않으며 다음 명령을 안내.

Dockerfile 방식:
  dcraft init --from dockerfile --service-name app --context . --dockerfile Dockerfile \
    --port 8080:80 --volume ./data:/data --restart unless-stopped

이미지 방식:
  dcraft init --from image --service-name web --image nginx:1.27-alpine --port 8080:80

기존 Compose 파일이 있으면 덮어쓰지 않음. 파일을 직접 확인하거나
--output <기존 파일> --approve file_overwrite를 함께 지정 필요.
Dockerfile의 EXPOSE/CMD는 자동으로 읽지 않음. 포트와 명령은 직접 입력한
값만 기록하며, 이미지/Dockerfile에서 포트나 데이터 경로를 추측하지 않음.`

	msgStageCollect  = "입력 확인"
	msgStagePreview  = "생성 미리보기"
	msgStageValidate = "Compose 검증"
	msgStageWrite    = "파일 저장"

	msgModeMissing    = "구성 방식 지정 필요."
	msgModeInvalid    = "구성 방식은 dockerfile 또는 image만 사용 가능."
	msgServiceMissing = "서비스 이름 지정 필요."
	msgServiceInvalid = "서비스 이름에 사용할 수 없는 문자가 있음."
	msgImageMissing   = "이미지 방식을 선택했다면 이미지 참조 지정 필요."
	msgImageInvalid   = "이미지 참조 형식이 올바르지 않음."
	msgImageModeOnly  = "이미지 방식에서는 --context와 --dockerfile을 사용하지 않음. 이미지 실행에 필요한 값만 지정."
	msgDockerfileMode = "Dockerfile 방식에서는 --image를 사용하지 않음. 빌드 결과 이름이 필요하면 Compose 파일을 직접 편집."
	msgPortInvalid    = "포트 형식이 올바르지 않음."
	msgVolumeInvalid  = "볼륨 형식이 올바르지 않음."
	msgEnvInvalid     = "환경변수 형식이 올바르지 않음."
	msgRestartInvalid = "restart 값이 올바르지 않음."
	msgOutputInvalid  = "저장 경로가 올바르지 않음."
	msgOutputSymlink  = "저장 경로가 심볼릭 링크. 링크 대상이나 다른 파일을 대신 바꾸지 않음."
	msgOutputDir      = "저장 경로가 폴더. 파일 경로 지정."
	msgOutputParent   = "저장 폴더 찾지 못함. dcraft는 폴더를 새로 만들지 않음."
	msgOutputSpecial  = "저장 경로가 일반 파일이 아님."

	msgContextMissing   = "빌드 컨텍스트 폴더 찾지 못함."
	msgContextNotDir    = "빌드 컨텍스트가 폴더가 아님."
	msgDockerfileMiss   = "Dockerfile 찾지 못함."
	msgDockerfileNotReg = "Dockerfile 경로가 일반 파일이 아님."

	msgExistingConfig    = "이미 Compose 설정이 있어 새 파일을 만들지 않았음."
	msgExistingAction    = "기존 파일을 그대로 사용하려면 dcraft status로 현재 상태 확인."
	msgExistingOverwrite = "덮어쓰려면 --output <기존 파일>과 --approve file_overwrite를 함께 지정."
	msgExistingUntouched = "dcraft는 기존 Compose 파일과 Dockerfile을 수정하지 않았음."

	msgComposeRejected    = "생성한 Compose 내용을 Compose가 거부. 파일을 저장하지 않았음."
	msgComposeRejectedAct = "표시된 Compose 오류의 필드를 수정한 뒤 다시 실행."
	msgComposeNoFile      = "저장된 파일은 없으며 기존 파일도 변경되지 않았음."
	msgComposeMissing     = "Docker Compose 찾지 못함. dcraft init은 생성 내용을 Compose로 검증."
	msgComposeMissingAct  = "Docker Compose를 설치한 뒤 다시 실행하거나, 설치를 승인해 dcraft install-compose를 사용."
	msgComposeOutput      = "Compose 검증 결과 해석 실패. 파일을 저장하지 않았음."
	msgComposeServiceMiss = "생성한 서비스가 Compose 검증 결과에 없음. 파일을 저장하지 않았음."
	msgComposeNameMiss    = "Compose가 프로젝트 이름 확인 실패. 파일을 저장하지 않았음."

	msgSaveFailed                 = "Compose 파일 저장 실패."
	msgSaveFailedAct              = "저장 폴더의 권한과 여유 공간 확인. 임시 파일은 정리되며 기존 파일은 저장이 완료되기 전까지 그대로."
	msgDestinationChanged         = "미리보기 후 저장 대상이 바뀌었음. 덮어쓰지 않았음."
	msgDestinationChangedActOne   = "저장 경로의 현재 파일을 확인한 뒤 다시 미리보기."
	msgDestinationChangedActTwo   = "덮어쓰려면 --output <기존 파일>과 --approve file_overwrite를 함께 지정."
	msgDestinationChangedActThree = "새로 생긴 파일은 변경하지 않았음."

	msgToolMissing = "Docker CLI 찾지 못함."
	msgToolAction  = "Docker를 설치한 뒤 다시 실행."

	msgNextCommand = "dcraft up"
	msgNoRun       = "저장은 실행이 아님. dcraft는 저장 후 자동으로 up을 실행하지 않았음."
	msgRunInDir    = "다음 명령은 프로젝트 폴더에서 실행."

	msgNoticeEnvPlain    = "environment 값은 compose.yaml에 평문으로 저장됨. 비밀값은 여기에 입력하지 말고 dcraft secrets 참조 사용."
	msgNoticeExpose      = "Dockerfile의 EXPOSE/CMD는 자동으로 사용하지 않았음. 포트와 명령은 직접 입력한 값만 기록."
	msgNoticeEditable    = "생성된 파일은 일반 Compose 파일. dcraft 없이도 편집하고 실행 가능."
	msgNoticeOutside     = "저장 경로가 프로젝트 폴더 밖. 명시한 경로에만 저장하며 다른 폴더는 건드리지 않음."
	msgNoticeOverwrite   = "기존 파일을 새 내용으로 덮어씀. 이전 내용은 백업되지 않음."
	msgNoticeProjectName = "명시한 -p/--project-name 값은 생성 파일에 기록되지 않음. dcraft 실행 시 같은 값을 지정하거나 자동 이름 배정 사용."
	msgNoticeComposeName = "Compose가 폴더와 저장 위치에서 파생한 프로젝트 이름. dcraft 실행 시 충돌하면 가장 작은 번호 접미사가 붙을 수 있음."

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
	msgPreviewNoSave     = "아직 저장하지 않았음. 저장 여부 확인."
	msgPreviewInDir      = "프로젝트 폴더에서 실행."

	msgWizardTitle          = "최초 Compose 설정"
	msgWizardModeTitle      = "구성 방식"
	msgWizardModePrompt     = "Dockerfile을 빌드할지, 직접 입력한 이미지를 실행할지 선택. 포트와 데이터 경로는 자동으로 추측하지 않음."
	msgWizardModeDocker     = "Dockerfile로 빌드"
	msgWizardModeImage      = "이미지 직접 입력"
	msgWizardServiceTitle   = "서비스 이름"
	msgWizardServicePrompt  = "Compose 서비스 이름 입력. 기본값 app은 제안일 뿐이며 미리보기에서 확인."
	msgWizardServiceHint    = "예: nginx · node-api · postgres"
	msgWizardContextTitle   = "빌드 컨텍스트"
	msgWizardContextPrompt  = "Dockerfile 빌드에 보낼 폴더. 상대 경로는 프로젝트 폴더 기준이며 새 폴더를 만들지 않음."
	msgWizardContextHint    = "예: . · ./api · ./services/web"
	msgWizardDockerTitle    = "Dockerfile 경로"
	msgWizardDockerPrompt   = "빌드 컨텍스트 기준 Dockerfile 경로. 파일이 실제로 있어야 함."
	msgWizardDockerHint     = "예: Dockerfile · docker/Dockerfile · Dockerfile.production"
	msgWizardImageTitle     = "이미지 참조"
	msgWizardImagePrompt    = "실행할 이미지 참조 입력. 태그나 digest 포함 가능."
	msgWizardImageHint      = "예: nginx:1.27-alpine · postgres:16 · redis@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	msgWizardOutputTitle    = "저장 경로"
	msgWizardOutputPrompt   = "파일을 저장할 경로. 상대 경로는 프로젝트 폴더 기준이며 폴더는 새로 만들지 않음."
	msgWizardOutputHint     = "예: compose.yaml · compose.prod.yaml · deploy/compose.yaml"
	msgWizardPortTitle      = "포트"
	msgWizardPortPrompt     = "공개할 포트 입력. 비우면 포트를 추가하지 않음."
	msgWizardPortHint       = "한 줄에 하나 · 예: 8080:80 · 127.0.0.1:5432:5432 · 8000-8002:8000-8002"
	msgWizardVolumeTitle    = "볼륨"
	msgWizardVolumePrompt   = "마운트 입력. 비우면 볼륨을 추가하지 않음."
	msgWizardVolumeHint     = "한 줄에 하나 · 예: ./html:/usr/share/nginx/html:ro · pgdata:/var/lib/postgresql/data · ./cache:/cache:ro,Z"
	msgWizardEnvTitle       = "환경변수"
	msgWizardEnvPrompt      = "KEY=VALUE를 입력. 값은 compose.yaml에 평문으로 저장되며 비밀값은 입력 금지."
	msgWizardEnvHint        = "한 줄에 하나 · 예: NODE_ENV=production · POSTGRES_DB=app · ALLOWED_HOSTS=web,api"
	msgWizardCommandTitle   = "명령"
	msgWizardCommandPrompt  = "이미지의 기본 CMD를 덮어쓸 명령. 비우면 이미지 기본값 사용."
	msgWizardCommandHint    = "한 줄에 하나 · 예: nginx -g 'daemon off;' · node server.js · postgres -c log_statement=all"
	msgWizardRestartTitle   = "재시작 정책"
	msgWizardRestartPrompt  = "컨테이너 재시작 정책 선택. 지정하지 않으면 Compose 기본값 사용."
	msgWizardRestartNone    = "지정 안 함 (Compose 기본값)"
	msgWizardRestartNo      = "no"
	msgWizardRestartAlways  = "always"
	msgWizardRestartUnless  = "unless-stopped"
	msgWizardRestartFailure = "on-failure"

	msgNoticeOtherExisting = "기존 Compose 파일이 여러 개 있음. 이번에는 선택한 파일만 바꾸며 나머지는 그대로 둠."
	msgNoticeOverride      = "기존 override 파일이 있음: %s. dcraft는 수정하지 않으며, 생성 후 Compose가 이 파일을 자동으로 병합."

	msgSaved        = "Compose 파일 저장."
	msgSavedPath    = "저장 경로"
	msgSavedService = "서비스"
	msgSavedCheck   = "검증"
	msgSavedChanged = "변경"
	msgSavedNew     = "새 파일을 만들었음."
	msgSavedReplace = "기존 파일을 덮어썼음."
	msgSavedNoOther = "다른 사용자 파일과 Dockerfile은 수정하지 않았음."

	msgCanceled    = "최초 구성이 중단되었음. 저장 단계가 시작된 뒤라면 대상 파일이 이미 바뀌었을 수 있으므로 내용 확인. 임시 파일은 남지 않음."
	msgCanceledAct = "대상 파일 내용을 확인하고 필요하면 같은 명령을 다시 실행."
)
