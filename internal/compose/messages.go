package compose

// Korean user-facing messages owned by the Compose environment checks.
const (
	msgDockerMissing            = "Docker CLI를 찾을 수 없습니다."
	msgDockerMissingActionOne   = "Docker Desktop(macOS) 또는 Docker Engine(Linux)을 설치하세요."
	msgDockerMissingActionTwo   = "설치 후 새 터미널에서 docker --version이 성공하는지 확인하세요."
	msgDockerMissingActionThree = "WSL2에서는 Docker Desktop의 WSL 통합을 켜세요."
	msgDockerNotExecutable      = "Docker CLI를 실행할 수 없습니다."
	msgStreamUnsupported        = "실시간 입출력 연결을 준비하지 못했습니다."
	msgStreamUnsupportedAction  = "dcraft를 다시 설치하거나 빌드한 뒤 실행하세요."

	msgEndpointUnknown          = "Docker 연결 대상을 확인할 수 없습니다."
	msgEndpointUnknownActionOne = "docker context ls로 현재 컨텍스트를 확인하세요."
	msgEndpointUnknownActionTwo = "DOCKER_HOST 환경 변수가 올바른 로컬 소켓을 가리키는지 확인하세요."

	msgRemoteEngine          = "원격 Docker Engine은 지원하지 않습니다."
	msgRemoteEngineActionOne = "이 컴퓨터의 Docker Desktop 또는 로컬 Engine을 사용하세요."
	msgRemoteEngineActionTwo = "DOCKER_HOST 또는 Docker 컨텍스트를 로컬 unix 소켓으로 변경하세요."

	msgComposeMissing            = "Docker Compose를 사용할 수 없습니다."
	msgComposeMissingActionOne   = "Docker Desktop에 포함된 Compose를 사용하거나 Compose 플러그인을 설치하세요."
	msgComposeMissingActionTwo   = "설치 위치와 버전을 확인한 뒤 다시 실행하세요."
	msgComposeMissingActionThree = "dcraft는 Docker를 자동으로 교체하거나 권한을 올리지 않습니다."
	msgComposeMissingActionFour  = "공식 배포물로 안전하게 설치하려면 dcraft install-compose 를 실행하세요. 설치 계획을 확인한 뒤 승인합니다."

	msgComposeUnsupported            = "설치된 Compose가 dcraft에 필요한 기능을 지원하지 않습니다."
	msgComposeTooOld                 = "설치된 Compose 버전이 너무 낮습니다."
	msgComposeUnsupportedActionOne   = "docker compose version으로 버전을 확인하세요."
	msgComposeUnsupportedActionTwo   = "Compose 플러그인을 공식 배포물로 업데이트하세요."
	msgComposeUnsupportedActionThree = "dcraft는 Docker 자체를 자동으로 교체하지 않습니다."

	msgEngineUnreachable            = "Docker Engine에 연결할 수 없습니다."
	msgEngineUnreachableActionOne   = "Docker Desktop 또는 Docker 서비스를 시작하세요."
	msgEngineUnreachableActionTwo   = "docker info 명령이 성공하는지 확인하세요."
	msgEngineUnreachableActionThree = "원격 서버용 DOCKER_HOST가 설정되어 있다면 해제하세요."

	msgEnginePermission            = "Docker Engine에 접근할 권한이 없습니다."
	msgEnginePermissionActionOne   = "Docker Desktop의 사용자 설정과 소켓 권한을 확인하세요."
	msgEnginePermissionActionTwo   = "Linux에서는 docker 그룹 소속과 /var/run/docker.sock 권한을 확인하세요."
	msgEnginePermissionActionThree = "dcraft는 관리자 권한 상승을 대신 수행하지 않습니다."

	msgNoConfiguration                 = "대상 폴더에서 Compose 설정을 찾지 못했습니다."
	msgNoConfigurationActionOne        = "dcraft init으로 Dockerfile 또는 이미지에서 compose.yaml을 만들거나, compose.yaml 또는 docker-compose.yml을 대상 폴더에 추가하세요."
	msgNoConfigurationActionTwo        = "다른 폴더를 대상으로 하려면 --path로 지정하세요."
	msgNoConfigurationActionThree      = "dcraft는 상위 폴더를 자동으로 탐색하지 않습니다."
	msgInvalidConfiguration            = "Compose 설정을 읽을 수 없습니다."
	msgInvalidConfigurationActionOne   = "docker compose config 명령으로 오류를 확인하세요."
	msgInvalidConfigurationActionTwo   = "파일 경로와 YAML 문법을 확인하세요."
	msgInvalidConfigurationActionThree = "dcraft는 기존 Compose 파일을 수정하지 않습니다."
	msgInspectOutput                   = "Compose 출력을 해석할 수 없습니다."
	msgInspectOutputAction             = "docker compose config와 docker compose ps 명령의 출력을 확인하세요."
	msgInspectPSFailed                 = "스택의 컨테이너 상태를 조회하지 못했습니다."
	msgInspectPSFailedActionOne        = "docker compose ps --all --format json을 직접 실행해 원인을 확인하세요."
	msgInspectPSFailedActionTwo        = "dcraft status로 다시 확인하세요."

	msgDerivedNameUnknown          = "Compose 프로젝트 기본 이름을 확인하지 못했습니다."
	msgDerivedNameUnknownActionOne = "docker compose version으로 Compose 버전을 확인하세요."
	msgDerivedNameUnknownActionTwo = "대상 Compose 파일과 프로젝트 폴더를 확인한 뒤 다시 실행하세요."
	msgConsumerListFailed          = "볼륨을 사용하는 컨테이너를 확인하지 못했습니다."
	msgConsumerListFailedAction    = "docker ps -a --filter volume=<볼륨 이름>을 직접 실행해 원인을 확인하세요. 확인할 수 없으면 dcraft는 백업을 진행하지 않습니다."
	msgProjectListFailed           = "기존 Compose 프로젝트 목록을 확인하지 못했습니다."
	msgProjectListFailedActionOne  = "docker compose ls -a --format json을 직접 실행해 원인을 확인하세요."
	msgProjectListFailedActionTwo  = "이름 충돌을 확인할 수 없으므로 dcraft는 스택을 변경하지 않았습니다."

	msgDataVolumeInspectFailed = "대상 볼륨 상태를 확인하지 못했습니다."
	msgDataVolumeInspectAction = "docker volume inspect <볼륨 이름>을 직접 실행해 원인을 확인하세요. 확인할 수 없으면 dcraft는 데이터를 교체하지 않습니다."
)

const (
	msgSecretCleanupFailed = "스택은 제거했지만 dcraft 관리 비밀값 전달 파일을 모두 정리하지 못했습니다. 상태 폴더(DCRAFT_STATE_DIR)의 권한을 확인하세요."
)
