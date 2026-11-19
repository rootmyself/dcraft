package secrets

// Korean user-facing text owned by the secrets feature. Command names, flags,
// phase/delivery/source machine values and JSON keys stay English.
const (
	msgTitle   = "비밀값 연결"
	msgSummary = "비밀값 원본 참조를 저장·조회·제거합니다."
	msgUsage   = `dcraft secrets [--add | --remove | --list] [--json]
  dcraft secrets --add -s <서비스> --phase runtime --delivery environment|file --source env|file --ref <참조> [--target <이름>]
  dcraft secrets --add -s <서비스> --phase build --source env|file --ref <참조> [--target <BuildKit id>]
  dcraft secrets --add -s <서비스> --phase runtime|build --source aws --ref <secret 이름|ARN> --target <이름> [--field <JSON 경로>] [--region <리전>] [--aws-profile <프로필>]
  dcraft secrets --remove -s <서비스> --delivery environment|file --target <이름>
  dcraft secrets

저장되는 것은 원본 위치와 전달 방식뿐이며 해석된 비밀값은 저장하지 않습니다.
실행 시점 환경변수 전달 값은 Docker 조회 권한이 있는 사용자가 컨테이너 환경에서 읽을 수 있습니다.
실행 시점 파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 동작합니다.
빌드 시점은 항상 BuildKit secret mount로 전달하며 Dockerfile의 RUN --mount=type=secret,id=<target>가 값을 소비해야 합니다. dcraft는 Dockerfile을 자동 수정하지 않습니다.
AWS Secrets Manager 원본은 설치된 AWS CLI의 기존 자격 증명을 사용합니다. dcraft는 자격 증명을 저장하지 않으며 필요한 참조만 조회합니다.
대화형 화면에서는 서비스 → 시점 → 전달 방식 → 원본 참조 순서로 선택합니다. AWS를 고르면 field·region·profile을 선택적으로 입력합니다.`

	msgFlagAdd        = "비밀값 참조를 추가합니다."
	msgFlagRemove     = "비밀값 참조를 제거합니다."
	msgFlagList       = "비밀값 참조를 조회합니다 (기본 동작)."
	msgFlagPhase      = "전달 시점 (runtime 또는 build)"
	msgFlagDelivery   = "전달 방식 (runtime: environment|file, build: BuildKit mount로 고정)"
	msgFlagSource     = "원본 종류 (env, file, aws)"
	msgFlagRef        = "환경변수 이름, 프로젝트 기준 로컬 파일 경로 또는 AWS secret 이름/ARN"
	msgFlagTarget     = "전달 대상 이름 (환경변수 이름, /run/secrets 파일 이름 또는 BuildKit secret id)"
	msgFlagField      = "AWS SecretString에서 선택할 JSON dot 경로 (예: database.password)"
	msgFlagRegion     = "AWS 리전 (생략하면 AWS CLI 설정을 사용합니다)"
	msgFlagAWSProfile = "AWS CLI profile (--profile은 Compose profile 전용이라 --aws-profile을 사용합니다)"

	msgNoCompose        = "Compose 서비스를 확인할 수 없어 비밀값 참조를 저장하지 않았습니다."
	msgNoComposeAction  = "Compose 설정과 Docker 상태를 확인한 뒤 다시 실행하세요."
	msgNoServices       = "Compose 설정에 서비스가 없습니다."
	msgNoServicesAction = "Compose 설정에 서비스를 추가한 뒤 다시 실행하세요."

	msgActionConflict        = "한 번에 하나의 동작만 지정하세요: --add, --remove, --list"
	msgOneService            = "서비스 하나를 -s/--service로 지정하세요."
	msgMissingInput          = "필요한 입력이 없습니다: %s"
	msgRemoveNeeds           = "참조를 제거하려면 --delivery와 --target을 지정하세요."
	msgRemoveMissing         = "해당 비밀값 참조를 찾지 못했습니다: 서비스 %s, %s, 대상 %s"
	msgAdded                 = "비밀값 참조를 저장했습니다."
	msgRemoved               = "비밀값 참조를 제거했습니다."
	msgRemovedFileNotice     = "파일 전달 값은 다음 성공적인 dcraft up(서비스 재생성) 또는 dcraft down까지 상태 폴더에 남습니다. 그래야 실행 중인 컨테이너가 그 사이에도 재시작될 수 있습니다."
	msgRemovedBuildNotice    = "빌드 시점 비밀값 원본은 다음 성공적인 빌드 또는 dcraft down까지 상태 폴더에 남습니다. 다음 빌드에서 다시 준비되며 이전 값은 저장·비교하지 않습니다."
	msgOverrideFailed        = "참조는 저장했지만 dcraft 관리 Compose override를 갱신하지 못했습니다."
	msgBuildModelUnavailable = "빌드 서비스 구성을 확인할 수 없어 빌드 시점 비밀값 참조를 저장하지 않았습니다."
	msgBuildServiceMissing   = "서비스 '%s'에는 Compose build 설정이 없어 빌드 시점 비밀값을 저장할 수 없습니다."
	msgBuildServiceUnknown   = "서비스를 Compose 빌드 구성에서 찾지 못했습니다: %s"
	msgBuildServiceAction    = "Compose 파일에서 build: 항목이 있는 서비스에만 phase: build 참조를 추가하세요."
	msgUnknownServiceAction  = "dcraft status로 현재 Compose 서비스 이름을 확인하세요."
	msgOverrideAction        = "다음 dcraft up에서 override를 다시 만들 수 있도록 상태 폴더 권한을 확인하세요."

	msgInputActionOne = "dcraft secrets --add -s <서비스> --delivery environment|file --source env|file --ref <참조>"
	msgInputActionTwo = "dcraft secrets로 현재 참조 목록을 확인하세요."

	msgWizardTitle          = "비밀값 연결 설정"
	msgWizardServiceTitle   = "서비스 선택"
	msgWizardServicePrompt  = "비밀값을 전달할 Compose 서비스를 선택하세요."
	msgWizardPhaseTitle     = "전달 시점 선택"
	msgWizardPhasePrompt    = "값이 필요한 시점을 선택하세요. 실행 시점은 컨테이너 실행, 빌드 시점은 이미지 빌드에서 사용합니다."
	msgWizardPhaseRuntime   = "실행 시점 (runtime)"
	msgWizardPhaseBuild     = "빌드 시점 (build, BuildKit secret mount)"
	msgWizardDeliveryTitle  = "전달 방식 선택"
	msgWizardDeliveryPrompt = "실행 시점의 전달 방식을 선택하세요. 빌드 시점을 선택했다면 전달 방식은 항상 BuildKit secret mount로 고정되며 이 선택은 사용하지 않습니다."
	msgWizardDeliveryEnv    = "환경변수 (environment)"
	msgWizardDeliveryFile   = "서비스 파일 /run/secrets (file)"
	msgWizardSourceTitle    = "원본 종류 선택"
	msgWizardSourcePrompt   = "값을 읽어올 원본을 선택하세요. AWS를 고르면 기존 AWS CLI 인증을 사용합니다."
	msgWizardSourceEnv      = "환경변수 (env)"
	msgWizardSourceFile     = "로컬 파일 (file)"
	msgWizardSourceAWS      = "AWS Secrets Manager (aws)"
	msgWizardRefTitle       = "원본 참조 입력"
	msgWizardRefPrompt      = "원본 참조를 입력하세요."
	msgWizardRefHint        = "환경변수 이름, 프로젝트 폴더 기준 파일 경로 또는 AWS secret 이름/ARN (예: DB_PASSWORD, secrets/token.txt, prod/db)"
	msgWizardTargetTitle    = "전달 대상 이름 입력"
	msgWizardTargetPrompt   = "AWS 원본은 전달 대상 이름을 직접 지정해야 합니다. 컨테이너 환경변수 이름 또는 /run/secrets 파일 이름을 입력하세요."
	msgWizardTargetHint     = "예: DB_PASSWORD, worker_token. secret 이름/ARN에서 자동으로 만들지 않습니다."
	msgWizardFieldTitle     = "AWS field 선택"
	msgWizardFieldPrompt    = "구조화된 SecretString에서 전달할 값을 고르세요. 텍스트 전체를 쓸 때는 비워 두고 Enter를 누르세요."
	msgWizardFieldHint      = "JSON dot 경로 (예: database.password). 키에 '.'이 있으면 지원하지 않습니다."
	msgWizardRegionTitle    = "AWS 리전 선택"
	msgWizardRegionPrompt   = "AWS 리전을 입력하세요. AWS CLI 설정을 그대로 쓸 때는 비워 두고 Enter를 누르세요."
	msgWizardRegionHint     = "예: ap-northeast-2. 비우면 AWS CLI 기본 리전을 사용합니다."
	msgWizardProfileTitle   = "AWS profile 선택"
	msgWizardProfilePrompt  = "AWS CLI profile을 입력하세요. 기본 자격 증명 체인을 쓸 때는 비워 두고 Enter를 누르세요."
	msgWizardProfileHint    = "예: ops-admin. dcraft는 자격 증명을 저장하지 않습니다."

	msgPreviewTitle           = "저장할 비밀값 연결"
	msgPreviewService         = "서비스"
	msgPreviewPhase           = "시점"
	msgPreviewDelivery        = "전달"
	msgPreviewSource          = "원본"
	msgPreviewField           = "AWS field"
	msgPreviewRegion          = "AWS 리전"
	msgPreviewProfile         = "AWS profile"
	msgPreviewEffect          = "효과"
	msgPreviewNote            = "dcraft는 해석된 값을 .dcraft.yaml이나 이력에 저장하지 않습니다."
	msgPreviewEnvVisible      = "환경변수 전달 값은 Docker 조회 권한을 가진 사용자가 컨테이너 환경에서 읽을 수 있습니다."
	msgPreviewFileApp         = "파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 동작합니다."
	msgPreviewBuildFixed      = "빌드 시점은 전달 방식이 BuildKit secret mount로 고정됩니다. 위에서 고른 환경변수/파일 방식 대신 Dockerfile의 RUN --mount=type=secret로 전달합니다."
	msgPreviewBuildDockerfile = "Dockerfile에 RUN --mount=type=secret,id=<target> ... 를 추가해야 값이 전달됩니다. dcraft는 Dockerfile을 자동으로 수정하지 않습니다."
	msgPreviewBuildCache      = "값 변경은 Docker 빌드 캐시를 무효화하지 않습니다. 새 값을 반영하려면 dcraft update --no-cache를 사용하세요."
	msgPreviewAWS             = "AWS Secrets Manager 값은 설치된 AWS CLI의 기존 자격 증명으로 조회합니다. 자격 증명은 dcraft 설정에 저장하지 않으며, 현재 작업이 참조하는 원본만 원본별 한 번 조회합니다. 바이너리 전용 원본은 지원하지 않습니다."

	msgPhaseRuntime       = "실행 시점 (runtime)"
	msgPhaseBuild         = "빌드 시점 (build)"
	msgDeliveryEnvLabel   = "환경변수 %s"
	msgDeliveryFileLabel  = "파일 %s"
	msgDeliveryBuildLabel = "BuildKit secret mount %s"
	msgSourceEnv          = "환경변수"
	msgSourceFile         = "로컬 파일"
	msgSourceAWS          = "AWS Secrets Manager"

	msgEffectEnv     = "dcraft up/restart 시 환경변수 %s 값으로 전달합니다."
	msgEffectFile    = "dcraft up/restart 시 파일 %s 경로로 전달합니다."
	msgEffectBuild   = "dcraft update의 로컬 빌드 시 BuildKit secret mount id=%s로 전달하고 Dockerfile의 RUN --mount에서 %s 경로로 읽습니다."
	msgEffectUnknown = "전달 방식을 확인할 수 없습니다."

	msgEnvVisible      = "환경변수 전달 값은 Docker 조회 권한이 있는 사용자가 컨테이너 환경에서 읽을 수 있습니다."
	msgFileApp         = "파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 하며, dcraft는 앱의 파일 소비를 보장하지 않습니다."
	msgBuildFixed      = "빌드 시점은 BuildKit secret mount로만 전달합니다. dcraft는 ARG·ENV로 조용히 대체하지 않습니다."
	msgBuildDockerfile = "Dockerfile이 RUN --mount=type=secret,id=<target>로 값을 소비해야 합니다. dcraft는 Dockerfile을 자동으로 수정하지 않습니다. 필요한 예시만 안내합니다."
	msgBuildCache      = "비밀값 변경은 Docker 빌드 캐시를 무효화하지 않습니다. 새 값을 반영하려면 dcraft update --no-cache로 다시 빌드하세요. dcraft는 이전 값을 저장하거나 비교하지 않습니다."
	msgBuildNoFallback = "dcraft는 값을 이미지 설정이나 history에 넣지 않지만, Dockerfile이 값을 출력하거나 이미지에 복사하는 동작은 막을 수 없습니다."

	msgAWSCredentialDisclosure = "AWS Secrets Manager 원본은 설치된 AWS CLI의 기존 자격 증명(환경변수, 공유 설정, SSO, 인스턴스 역할)을 사용합니다. dcraft는 자격 증명을 설정·상태·생성 스크립트에 저장하지 않습니다."
	msgAWSFetchPolicy          = "dcraft는 현재 작업이 실제 참조하는 AWS 원본만 조회하며, 한 작업에서 같은 원본은 한 번만 조회합니다. 선언만 된 다른 참조는 조회하지 않고, 조회한 값을 다음 작업으로 저장하거나 비교하지 않습니다."

	msgListTitle         = "비밀값 연결"
	msgListEmpty         = "저장된 비밀값 참조가 없습니다."
	msgListConfig        = "설정 파일"
	msgListOverride      = "dcraft 관리 실행 시점 Compose override"
	msgListBuildOverride = "dcraft 관리 빌드 시점 Compose override"
	msgListSource        = "원본"
	msgListEffect        = "효과"
	msgListNotice        = "알림"
	msgListAddHint       = "추가: dcraft secrets --add -s <서비스> --phase runtime --delivery environment|file --source env|file|aws --ref <참조> (AWS는 --target 필수, 선택: --field/--region/--aws-profile, 빌드는 --phase build)"
	msgListTUIHint       = "대화형 화면에서는 안내에 따라 선택할 수 있습니다."
	msgMigrationGuide    = "기존 Compose 파일이나 .env에 직접 적은 민감값은 dcraft가 자동으로 찾아 제거하지 않습니다. 원본을 참조로 옮기려면 dcraft secrets를 사용하세요. Docker 자체의 저장·조회 경계는 dcraft가 통제하지 않습니다."
)

const (
	msgStageValidate   = "입력 확인"
	msgStageSave       = "참조 저장"
	msgStageRemove     = "참조 제거"
	msgStageList       = "참조 조회"
	msgStageRegenerate = "전달 설정 갱신"
	msgStageWizard     = "설정 단계 준비"
)
