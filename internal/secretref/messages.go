package secretref

// Korean user-facing messages owned by secret reference configuration.
const (
	msgConfigUnreadable      = "비밀값 참조 설정을 읽을 수 없음."
	msgConfigMalformed       = "비밀값 참조 설정의 형식이 올바르지 않음."
	msgConfigRoot            = "비밀값 참조 설정의 최상위는 항목 묶음이어야 함."
	msgSecretsNotList        = "secrets 항목은 참조 목록이어야 함."
	msgSecretNotMapping      = "비밀값 참조 항목은 항목 묶음이어야 함."
	msgSecretFieldKey        = "비밀값 참조의 항목 이름은 문자열이어야 함."
	msgSecretUnknownField    = "비밀값 참조에 알 수 없는 항목이 있음: %s"
	msgSecretCredentialField = "자격 증명에 해당하는 항목은 저장하지 않음: %s"
	msgSecretDuplicateField  = "비밀값 참조의 항목이 중복되었음: %s"
	msgSecretFieldString     = "비밀값 참조의 %s 값은 문자열이어야 함."
	msgSecretFieldMissing    = "비밀값 참조에 필요한 항목이 없음: %s"
	msgDuplicateTarget       = "서비스 '%s'의 %s 전달 대상 '%s'가 중복되었음."
	msgUnknownService        = "비밀값 참조의 서비스가 Compose 설정에 없음: %s"
	msgAvailableServices     = "사용 가능한 서비스: "
	msgRemoveNotFound        = "해당 비밀값 참조 찾지 못함: 서비스 %s, %s, 대상 %s"
	msgInputInvalid          = "비밀값 참조 입력이 올바르지 않음."
	msgConfigEncode          = "비밀값 참조 설정을 저장할 형식으로 만들지 못함."
	msgConfigWrite           = "비밀값 참조 설정 저장 실패."
)

var (
	msgConfigActionOne         = ".dcraft.yaml의 secrets 항목 확인."
	msgConfigActionTwo         = "phase·delivery·source 값과 서비스·대상 이름이 올바른지 확인."
	msgDuplicateAction         = "같은 서비스와 전달 방식에서 대상 이름을 하나만 사용."
	msgUnknownServiceActionOne = "dcraft status로 현재 Compose 서비스 이름 확인."
	msgUnknownServiceActionTwo = "비밀값 참조의 service를 Compose에 있는 서비스 이름으로 지정."
	msgInputActionOne          = "--service, --delivery, --source, --ref에 필요한 값 지정."
	msgInputActionTwo          = "사용법: dcraft secrets --add -s <서비스> --delivery environment|file --source env|file --ref <참조>"
	msgListAction              = "dcraft secrets로 현재 참조 목록 확인."
)

const (
	msgRedacted = "[redacted]"

	msgEnvSourceMissing     = "환경변수 원본을 찾을 수 없음: %s"
	msgEnvSourceInvalid     = "환경변수 원본 값 사용 불가: %s"
	msgFileSourceMissing    = "비밀값 원본 파일을 찾을 수 없음: %s"
	msgFileSourceUnreadable = "비밀값 원본 파일을 읽을 수 없음: %s"
	msgFileSourceInvalid    = "비밀값 원본이 올바른 파일이 아님: %s"
	msgFileSourceEmpty      = "비밀값 원본 파일이 비어 있음: %s"

	msgAppliedEnv         = "환경변수로 전달한 비밀값: %s"
	msgAppliedFile        = "파일로 전달한 비밀값: %s (컨테이너의 /run/secrets 아래 경로)"
	msgEnvVisible         = "환경변수 전달 값은 Docker 조회 권한을 가진 사용자가 컨테이너 환경에서 읽을 수 있음. dcraft는 값을 출력하거나 저장하지 않음."
	msgFileAppSupport     = "파일 전달은 앱이 /run/secrets/<target> 경로를 직접 읽어야 동작. dcraft가 앱의 파일 읽기를 보장하지는 않음."
	msgFilesEnsured       = "재시작에 필요한 비밀값 파일 전달 상태 확인."
	msgStateInsideProject = "상태 폴더가 프로젝트 폴더 안에 있어 비밀값 파일을 안전하게 보관 불가."

	msgApplyActionOne      = "Docker 상태와 상태 폴더(DCRAFT_STATE_DIR) 권한·여유 공간 확인."
	msgApplyActionTwo      = "해결한 뒤 dcraft up을 다시 실행."
	msgEnvSourceActionOne  = "원본 환경변수가 설정된 셸에서 실행하거나, 참조 이름 확인."
	msgEnvSourceActionTwo  = "dcraft는 빈 값이나 없는 환경변수를 비밀값으로 전달하지 않음."
	msgFileSourceActionOne = "원본 파일 경로가 프로젝트 폴더 기준으로 실제 존재하는지 확인."
	msgFileSourceActionTwo = "읽기 권한이 있는 비어 있지 않은 일반 파일 지정."
)

const (
	msgAppliedOverride        = "dcraft 관리 Compose override: %s (사용자 Compose 파일은 수정하지 않았음.)"
	msgPartialOverwrite       = "비밀값 전달 준비가 실패했지만 이미 존재하던 파일 일부가 새 값으로 갱신되었을 수 있음. dcraft는 이전 값을 저장하지 않아 되돌리지 않음."
	msgPartialOverwriteAction = "dcraft status로 현재 상태를 확인하고, 필요한 경우 dcraft up을 다시 실행."
)

// Build-time delivery (ticket #10). The values reach BuildKit only through a
// secret mount; dcraft never falls back to ARG/ENV or a command line.
const (
	msgBuildServiceInvalid       = "서비스 '%s'에는 Compose build 설정이 없어 빌드 시점 비밀값 전달 불가."
	msgBuildServiceInvalidAction = "build: 항목이 있는 서비스에만 phase: build 참조 추가."
	msgBuildApplied              = "빌드 시점 비밀값을 BuildKit secret mount로 준비: %s (dcraft 관리 Compose override: %s)"
	msgBuildCacheNotice          = "Docker 빌드 캐시는 비밀값 내용을 캐시 키에 포함하지 않음. 원본 값을 바꾼 뒤에는 dcraft update --no-cache로 다시 빌드해야 새 값이 반영됨. dcraft는 이전 값을 저장하거나 비교하지 않음."
	msgBuildNoFallback           = "빌드 시점 비밀값은 BuildKit secret mount로만 전달. ARG·ENV로 조용히 대체하지 않음."
	msgBuildImageDisclosure      = "dcraft는 값을 이미지 설정이나 history에 넣지 않지만, Dockerfile이 값을 출력하거나 이미지에 복사하는 동작은 막을 수 없음."
	msgBuildMountMissing         = "서비스 '%s'의 Dockerfile(%s)에 BuildKit secret mount id=%s 참조가 없음. Dockerfile의 해당 RUN에 다음을 추가해야 값이 전달됨: RUN --mount=type=secret,id=%s ... . dcraft는 Dockerfile을 자동으로 수정하지 않음."
	msgBuildDockerfileUnreadable = "서비스 '%s'의 Dockerfile(%s)을 읽지 못해 BuildKit secret mount 참조 확인 실패: %v. Dockerfile에 RUN --mount=type=secret,id=%s 가 있는지 직접 확인."
	msgBuildDockerfileUnknown    = "서비스 '%s'의 Dockerfile 경로를 확인하지 못해 BuildKit secret mount 참조 확인 실패. Dockerfile에 RUN --mount=type=secret,id=%s 가 있는지 직접 확인."
	msgBuildFinishFailed         = "빌드 후 환경변수 원본 임시 파일 정리 실패. 상태 폴더의 build-secrets 파일 권한과 여유 공간 확인."
	msgBuildEnvLifetime          = "환경변수 원본 빌드 비밀값은 빌드 명령이 실행되는 동안에만 상태 폴더의 0600 파일로 존재하고, 빌드가 끝나면 삭제됨. 파일 원본은 다음 빌드를 위해 남으며 down 또는 성공적인 빌드 후 정리됨."
)

// AWS Secrets Manager source (ticket #10). The credential chain belongs to the
// AWS CLI; dcraft never stores or prints credentials or fetched values.
const (
	msgAWSValueEmpty         = "AWS Secrets Manager 원본 값이 비어 있음."
	msgAWSValueAbsent        = "AWS Secrets Manager 원본에 사용할 수 있는 SecretString이 없음."
	msgAWSValueInvalid       = "AWS Secrets Manager 원본 값 사용 불가."
	msgAWSCLIMissing         = "AWS CLI를 찾을 수 없음."
	msgAWSCredentialsMissing = "AWS 자격 증명을 찾을 수 없음."
	msgAWSRegionMissing      = "AWS 리전 확인 불가."
	msgAWSAccessDenied       = "AWS Secrets Manager 원본에 접근할 권한이 없음."
	msgAWSSecretNotFound     = "AWS Secrets Manager 원본을 찾을 수 없음."
	msgAWSNetwork            = "AWS Secrets Manager에 연결 실패."
	msgAWSFieldInvalid       = "AWS Secrets Manager 원본에서 field 경로 선택 불가: %s"
	msgAWSFailed             = "AWS Secrets Manager 조회에 실패."
)

const (
	msgAWSCLIActionOne            = "AWS CLI v2를 설치하고 PATH에서 실행 가능한지 확인."
	msgAWSCLIActionTwo            = "dcraft는 AWS CLI를 자동으로 설치하지 않음."
	msgAWSCredentialsActionOne    = "셸의 AWS_* 환경변수, ~/.aws 설정, SSO 로그인 또는 --aws-profile로 기존 자격 증명을 준비."
	msgAWSCredentialsActionTwo    = "dcraft는 자격 증명을 저장하지 않으며 설정 파일에 넣지 않음."
	msgAWSRegionActionOne         = "AWS 설정에 기본 리전을 지정하거나 --region으로 참조의 리전 저장."
	msgAWSRegionActionTwo         = "AWS CLI에서 aws configure로 리전을 설정할 수도 있음."
	msgAWSAccessDeniedActionOne   = "IAM에서 secretsmanager:GetSecretValue 권한 확인."
	msgAWSAccessDeniedActionTwo   = "KMS로 암호화된 원본이면 해당 키의 복호화 권한도 필요."
	msgAWSSecretNotFoundActionOne = "원본 이름 또는 ARN과 리전이 정확한지 확인."
	msgAWSSecretNotFoundActionTwo = "삭제 예정이거나 다른 계정의 원본이면 dcraft 설정 갱신."
	msgAWSNetworkActionOne        = "네트워크, 프록시, AWS 엔드포인트 연결을 확인한 뒤 다시 실행."
	msgAWSNetworkActionTwo        = "일시적인 장애라면 잠시 후 다시 시도."
	msgAWSValueActionOne          = "원본에 비어 있지 않은 텍스트 SecretString을 저장. 바이너리 전용 원본은 지원하지 않음."
	msgAWSValueActionTwo          = "구조화된 값을 쓸 때는 비어 있지 않은 스칼라를 가리키는 --field 경로 지정."
	msgAWSFieldActionOne          = "원본 SecretString이 JSON인지, --field 경로의 키·배열 위치가 정확한지 확인."
	msgAWSFieldActionTwo          = "field는 한 개의 스칼라 값(문자열·숫자·true/false)만 가리킬 수 있음."
	msgAWSFailedActionOne         = "AWS CLI로 같은 원본을 직접 조회해 오류 내용 확인."
	msgAWSFailedActionTwo         = "자격 증명·리전·권한을 확인한 뒤 dcraft up 또는 dcraft update를 다시 실행."
	msgAWSFetchedRuntime          = "AWS Secrets Manager에서 실행 시점 참조 %d개를 조회. 값은 전달에만 사용하고 저장하지 않음."
	msgAWSFetchedBuild            = "AWS Secrets Manager에서 빌드 시점 참조 %d개를 조회. 값은 BuildKit secret 파일로만 사용하고 저장하지 않음."
	msgAWSCredentialDisclosure    = "AWS 자격 증명은 기존 AWS CLI 인증 수단 사용. dcraft는 자격 증명을 저장하거나 출력하지 않음."
)

const msgBuildAWSLifetime = "AWS Secrets Manager 빌드 비밀값은 빌드 명령이 실행되는 동안에만 상태 폴더의 0600 파일로 존재하고, 빌드가 끝나면 삭제됨. 다음 빌드에서 기존 AWS 인증으로 다시 조회하며 이전 값을 저장하거나 비교하지 않음."

const (
	msgMaterializedMissing       = "재시작에 필요한 비밀값 전달 파일이 없음: %s:%s"
	msgMaterializedMissingAction = "dcraft up으로 비밀값 전달 파일을 다시 준비한 뒤 restart를 실행. restart는 회전된 원본을 다시 읽지 않음."
)
