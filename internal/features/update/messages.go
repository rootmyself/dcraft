package update

// Korean user-facing registration text owned by the update feature.
const (
	msgTitle             = "갱신"
	msgSummary           = "현재 설정으로 로컬 이미지 빌드. 이미지 전용 서비스 취득 뒤 변경된 컨테이너만 적용. 제한 시간 안에 실행 상태 확인."
	msgUsage             = "dcraft update [--no-cache] [--build-pull] [--no-pull] [--force-recreate] [--timeout 초] [-s 서비스]"
	msgFlagNoCache       = "로컬 빌드 캐시를 사용하지 않음. 모든 빌드 단계를 다시 실행해 시간이 더 걸리며, 베이스 이미지 취득과는 다름."
	msgFlagBuildPull     = "로컬 빌드의 베이스 이미지를 새로 받음. 이미지 전용 서비스의 태그 취득과는 다름."
	msgFlagNoPull        = "이미지 전용 서비스의 지정 태그를 새로 받지 않음. 로컬에 없는 이미지는 변경 전에 실패."
	msgFlagForceRecreate = "변경이 없어도 대상 컨테이너를 모두 다시 만듦. 짧은 중단이 늘어남."
	msgFlagTimeout       = "갱신 후 실행 상태를 확인할 최대 시간(정수 초, 1~3600). healthcheck가 없으면 'running, 앱 확인 안 됨'으로 표시."

	msgNoController     = "갱신 컨트롤러가 준비되지 않았음."
	msgNoControllerNext = "dcraft를 다시 빌드하거나 설치한 뒤 다시 실행."
)
