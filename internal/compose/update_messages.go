package compose

// Korean user-facing messages owned by the update adapter calls.
const (
	msgUpdateModelFailed   = "Compose 서비스 구성 읽지 못함."
	msgUpdateModelAction   = "docker compose config --format json이 성공하는지 확인."
	msgUpdateInspectFailed = "컨테이너 상태 읽지 못함."
	msgUpdateInspectAction = "docker ps와 docker inspect가 성공하는지 확인."
	msgUpdateTagFailed     = "이전 버전 보존 태그 만들지 못함."
	msgUpdateTagAction     = "Docker 이미지 저장 공간과 권한 확인."
)
