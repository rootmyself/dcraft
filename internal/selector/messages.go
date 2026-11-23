package selector

// Korean user-facing text for explicit service/instance selection. Command
// names, flag names and machine categories stay English.
const (
	msgUnknownService          = "서비스를 찾을 수 없음: %s"
	msgAvailableServices       = "사용 가능한 서비스: "
	msgUnknownServiceActionOne = "dcraft status로 현재 서비스 목록과 상태 확인."
	msgUnknownServiceActionTwo = "-s/--service에 현재 Compose 설정의 서비스 이름 지정."

	msgInstanceOutOfRange = "인스턴스 번호가 범위를 벗어났음: %d"
	msgInstanceActionOne  = "인스턴스 번호는 0부터 시작. 목록의 #번호 사용."
	msgInstanceActionTwo  = "dcraft status로 현재 인스턴스 목록 확인."

	msgInstanceListHeader = "%s 인스턴스 (0부터 시작):"
	msgNoInstances        = "%s 서비스에는 컨테이너 인스턴스가 없음."

	msgServiceRequired          = "실행할 서비스 하나 지정 필요."
	msgMultipleServices         = "한 번에 하나의 서비스에만 접속 가능: %s"
	msgServiceRequiredActionOne = "dcraft exec -s <서비스> -i <번호> -- <명령> [인자...]"
	msgServiceRequiredActionTwo = "dcraft logs -s <서비스>로 로그를 먼저 확인 가능."

	msgServiceNotRunning     = "서비스가 실행 중이 아님: %s"
	msgChosenInstanceStopped = "선택한 인스턴스가 실행 중이 아님: %s (%s)"
	msgStoppedActionOne      = "dcraft up으로 스택을 시작한 뒤 다시 시도."
	msgStoppedActionTwo      = "dcraft status로 현재 서비스 상태 확인."

	msgInstanceAmbiguous      = "서비스에 실행 중인 인스턴스가 여러 개. 인스턴스 지정 필요: %s"
	msgExitAmbiguousActionOne = "dcraft exec -s <서비스> -i <번호> -- <명령>"

	msgIndexNeedsOneService          = "인스턴스를 지정하려면 로그 대상 서비스 하나만 선택 필요."
	msgIndexNeedsOneServiceActionOne = "dcraft logs -s <서비스> -i <번호>"
	msgIndexNeedsOneServiceActionTwo = "dcraft status로 서비스와 인스턴스 목록 확인."
)
