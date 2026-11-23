package compose

// Korean user-facing messages owned by the Compose environment checks.
const (
	msgDockerMissing            = "Docker CLI를 찾을 수 없음."
	msgDockerMissingActionOne   = "Docker Desktop(macOS) 또는 Docker Engine(Linux)을 설치."
	msgDockerMissingActionTwo   = "설치 후 새 터미널에서 docker --version이 성공하는지 확인."
	msgDockerMissingActionThree = "WSL2에서는 Docker Desktop의 WSL 통합 켜기."
	msgDockerNotExecutable      = "Docker CLI 실행 불가."
	msgStreamUnsupported        = "실시간 입출력 연결 준비 실패."
	msgStreamUnsupportedAction  = "dcraft를 다시 설치하거나 빌드한 뒤 실행."

	msgEndpointUnknown          = "Docker 연결 대상 확인 불가."
	msgEndpointUnknownActionOne = "docker context ls로 현재 컨텍스트 확인."
	msgEndpointUnknownActionTwo = "DOCKER_HOST 환경 변수가 올바른 로컬 소켓을 가리키는지 확인."

	msgRemoteEngine          = "원격 Docker Engine은 지원하지 않음."
	msgRemoteEngineActionOne = "이 컴퓨터의 Docker Desktop 또는 로컬 Engine을 사용."
	msgRemoteEngineActionTwo = "DOCKER_HOST 또는 Docker 컨텍스트를 로컬 unix 소켓으로 변경."

	msgComposeMissing            = "Docker Compose 사용 불가."
	msgComposeMissingActionOne   = "Docker Desktop에 포함된 Compose를 사용하거나 Compose 플러그인 설치."
	msgComposeMissingActionTwo   = "설치 위치와 버전을 확인한 뒤 다시 실행."
	msgComposeMissingActionThree = "dcraft는 Docker를 자동으로 교체하거나 권한을 올리지 않음."
	msgComposeMissingActionFour  = "공식 배포물로 안전하게 설치하려면 dcraft install-compose 를 실행. 설치 계획을 확인한 뒤 승인."

	msgComposeUnsupported            = "설치된 Compose가 dcraft에 필요한 기능을 지원하지 않음."
	msgComposeTooOld                 = "설치된 Compose 버전이 너무 낮음."
	msgComposeUnsupportedActionOne   = "docker compose version으로 버전 확인."
	msgComposeUnsupportedActionTwo   = "Compose 플러그인을 공식 배포물로 업데이트."
	msgComposeUnsupportedActionThree = "dcraft는 Docker 자체를 자동으로 교체하지 않음."

	msgEngineUnreachable            = "Docker Engine에 연결 불가."
	msgEngineUnreachableActionOne   = "Docker Desktop 또는 Docker 서비스 시작."
	msgEngineUnreachableActionTwo   = "docker info 명령이 성공하는지 확인."
	msgEngineUnreachableActionThree = "원격 서버용 DOCKER_HOST가 설정되어 있다면 해제."

	msgEnginePermission            = "Docker Engine에 접근할 권한이 없음."
	msgEnginePermissionActionOne   = "Docker Desktop의 사용자 설정과 소켓 권한 확인."
	msgEnginePermissionActionTwo   = "Linux에서는 docker 그룹 소속과 /var/run/docker.sock 권한 확인."
	msgEnginePermissionActionThree = "dcraft는 관리자 권한 상승을 대신 수행하지 않음."

	msgNoConfiguration                 = "대상 폴더에서 Compose 설정 찾지 못함."
	msgNoConfigurationActionOne        = "dcraft init으로 Dockerfile 또는 이미지에서 compose.yaml을 만들거나, compose.yaml 또는 docker-compose.yml을 대상 폴더에 추가."
	msgNoConfigurationActionTwo        = "다른 폴더를 대상으로 하려면 --path로 지정."
	msgNoConfigurationActionThree      = "dcraft는 상위 폴더를 자동으로 탐색하지 않음."
	msgInvalidConfiguration            = "Compose 설정을 읽을 수 없음."
	msgInvalidConfigurationActionOne   = "docker compose config 명령으로 오류 확인."
	msgInvalidConfigurationActionTwo   = "파일 경로와 YAML 문법 확인."
	msgInvalidConfigurationActionThree = "dcraft는 기존 Compose 파일을 수정하지 않음."
	msgInspectOutput                   = "Compose 출력 해석 불가."
	msgInspectOutputAction             = "docker compose config와 docker compose ps 명령의 출력 확인."
	msgInspectPSFailed                 = "스택의 컨테이너 상태 조회 실패."
	msgInspectPSFailedActionOne        = "docker compose ps --all --format json을 직접 실행해 원인 확인."
	msgInspectPSFailedActionTwo        = "dcraft status로 다시 확인."

	msgDerivedNameUnknown          = "Compose 프로젝트 기본 이름 확인 실패."
	msgDerivedNameUnknownActionOne = "docker compose version으로 Compose 버전 확인."
	msgDerivedNameUnknownActionTwo = "대상 Compose 파일과 프로젝트 폴더를 확인한 뒤 다시 실행."
	msgConsumerListFailed          = "볼륨을 사용하는 컨테이너 확인 실패."
	msgConsumerListFailedAction    = "docker ps -a --filter volume=<볼륨 이름>을 직접 실행해 원인 확인. 확인할 수 없으면 dcraft는 백업을 진행하지 않음."
	msgProjectListFailed           = "기존 Compose 프로젝트 목록 확인 실패."
	msgProjectListFailedActionOne  = "docker compose ls -a --format json을 직접 실행해 원인 확인."
	msgProjectListFailedActionTwo  = "이름 충돌을 확인할 수 없으므로 dcraft는 스택을 변경하지 않았음."

	msgDataVolumeInspectFailed = "대상 볼륨 상태 확인 실패."
	msgDataVolumeInspectAction = "docker volume inspect <볼륨 이름>을 직접 실행해 원인 확인. 확인할 수 없으면 dcraft는 데이터를 교체하지 않음."
)

const (
	msgSecretCleanupFailed = "스택은 제거했지만 dcraft 관리 비밀값 전달 파일을 모두 정리 실패. 상태 폴더(DCRAFT_STATE_DIR)의 권한 확인."
)
