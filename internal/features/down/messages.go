package down

const (
	msgTitle       = "스택 제거"
	msgSummary     = "스택 컨테이너와 기본 네트워크 제거."
	msgMenuLabel   = "서비스 제거"
	msgMenuSummary = "스택 컨테이너와 기본 네트워크 제거."
	msgUsage       = `dcraft down [--path <폴더>] [--profile <프로필>] [--json]

스택의 컨테이너와 기본 네트워크 제거. 프로젝트 전체에 적용되며 서비스 선택은 지원하지 않음.
데이터 볼륨은 삭제하지 않으므로 dcraft up으로 다시 시작하면 기존 데이터를 그대로 사용.
다른 Compose 프로젝트의 컨테이너·네트워크·볼륨은 건드리지 않음.`

	msgNoController     = "스택 제거 연결이 준비되지 않았음."
	msgNoControllerNext = "dcraft를 다시 설치하거나 빌드한 뒤 실행."
)
