package rollback

// Korean user-facing registration text owned by the rollback feature.
const (
	msgTitle       = "이전 버전 복원"
	msgSummary     = "직전 정상 갱신이 기록한 이미지와 실행 설정으로 돌아갑니다. 데이터·볼륨·외부 시스템·스크립트 부작용은 되돌리지 않고 배포 훅도 다시 실행하지 않습니다."
	msgUsage       = "dcraft rollback [--timeout 초] --approve previous_version"
	msgFlagTimeout = "복원 후 실행 상태를 확인할 최대 시간(정수 초, 1~3600). healthcheck가 없으면 'running, 앱 확인 안 됨'으로 표시합니다."

	msgNoController     = "이전 버전 복원 컨트롤러가 준비되지 않았습니다."
	msgNoControllerNext = "dcraft를 다시 빌드하거나 설치한 뒤 다시 실행하세요."
)
