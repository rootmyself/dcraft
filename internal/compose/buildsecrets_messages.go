package compose

// Korean user-facing messages for build-time secret delivery. Machine values
// (category, service, id) stay English.
const (
	msgBuildKitDisabled    = "빌드 시점 비밀값은 BuildKit secret mount가 필요하지만 DOCKER_BUILDKIT=0으로 실행 중입니다. dcraft는 ARG·ENV로 대체하지 않습니다."
	msgBuildKitUnavailable = "설치된 Docker 빌더에서 BuildKit secret mount를 사용할 수 없는 것으로 보입니다. dcraft는 값을 ARG·ENV나 명령줄로 대신 전달하지 않습니다."
	msgBuildKitActionOne   = "Docker Buildx/BuildKit이 포함된 Docker CLI·Compose를 사용하고 DOCKER_BUILDKIT=0을 해제하세요."
	msgBuildKitActionTwo   = "docker buildx version과 docker compose version이 성공하는지 확인한 뒤 다시 실행하세요."
	msgBuildFinishFailed   = "빌드 후 환경변수 원본 임시 파일을 정리하지 못했습니다. 상태 폴더의 build-secrets 권한과 여유 공간을 확인하세요."
	msgUpNoBuildSecrets    = "빌드 시점 비밀값이 설정된 서비스가 있어 dcraft up은 --no-build로 실행했습니다. dcraft는 비밀값 없이 암시적으로 빌드하지 않습니다. 이미지가 없으면 dcraft update로 먼저 빌드하세요."
)
