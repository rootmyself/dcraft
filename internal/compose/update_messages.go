package compose

// Korean user-facing messages owned by the update adapter calls.
const (
	msgUpdateModelFailed   = "Compose 서비스 구성을 읽지 못했습니다."
	msgUpdateModelAction   = "docker compose config --format json이 성공하는지 확인하세요."
	msgUpdateInspectFailed = "컨테이너 상태를 읽지 못했습니다."
	msgUpdateInspectAction = "docker ps와 docker inspect가 성공하는지 확인하세요."
	msgUpdateTagFailed     = "이전 버전 보존 태그를 만들지 못했습니다."
	msgUpdateTagAction     = "Docker 이미지 저장 공간과 권한을 확인하세요."
)
