package stop

const (
	msgTitle   = "스택 중지"
	msgSummary = "스택 또는 선택한 서비스 중지."
	msgUsage   = `dcraft stop [--service <서비스>] [--path <폴더>] [--profile <프로필>] [--json]

스택 전체 또는 -s/--service로 지정한 서비스의 컨테이너 중지.
컨테이너와 데이터는 유지되며 다시 시작하려면 dcraft up을 사용. 데이터 볼륨은 삭제하지 않음.`

	msgNoController     = "스택 중지 연결이 준비되지 않았음."
	msgNoControllerNext = "dcraft를 다시 설치하거나 빌드한 뒤 실행."
)
