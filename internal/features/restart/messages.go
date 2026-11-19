package restart

const (
	msgTitle   = "스택 재시작"
	msgSummary = "스택 컨테이너를 다시 시작합니다."
	msgUsage   = `dcraft restart [--service <서비스>] [--path <폴더>] [--profile <프로필>] [--json]

실행 중인 스택 전체 또는 -s/--service로 지정한 서비스의 컨테이너를 다시 시작합니다.
restart는 새 설정이나 새 이미지를 적용하지 않습니다. 설정을 바꾼 뒤에는 dcraft up을 사용하세요.
지정한 서비스만 재시작하며 의존 서비스는 재시작하지 않습니다.`

	msgNoController     = "스택 재시작 연결이 준비되지 않았습니다."
	msgNoControllerNext = "dcraft를 다시 설치하거나 빌드한 뒤 실행하세요."
)
