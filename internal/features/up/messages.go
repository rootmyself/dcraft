package up

const (
	msgTitle   = "스택 시작"
	msgSummary = "현재 폴더 스택 시작."
	msgUsage   = `dcraft up [--service <서비스>] [--path <폴더>] [--profile <프로필>] [--json]

현재 Compose 설정을 적용해 스택 컨테이너 시작. 중지된 스택을 다시 시작할 때도 사용.
전체 스택이 기본이며 -s/--service로 서비스 지정 가능. 지정한 서비스와 그 의존 서비스가 함께 시작될 수 있음.
짧은 서비스 중단이 있을 수 있고 전체 원자성을 보장하지 않음.`

	msgNoController     = "스택 시작 연결이 준비되지 않았음."
	msgNoControllerNext = "dcraft를 다시 설치하거나 빌드한 뒤 실행."
)
