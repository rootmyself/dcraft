package exec

const (
	// Input field keys consumed by WithInputs and rendered by the TUI.
	fieldInstance = "instance"
	fieldCommand  = "command"
)

// Korean user-facing text owned by the exec feature.
const (
	msgTitle   = "서비스 접속"
	msgSummary = "실행 중인 서비스 인스턴스에서 지정한 명령을 정확히 실행합니다."
	msgUsage   = "dcraft exec -s <서비스> [-i <인스턴스>] [-T] -- <명령> [인자...]\n" +
		"  -s/--service와 명령은 필수입니다. dcraft는 셸을 대신 실행하지 않으므로\n" +
		"  이미지 안에 해당 명령이 있어야 합니다.\n" +
		"  대화형 터미널에서는 입력·출력을 연결하고, -T 또는 비대화형에서는 연결하지 않습니다.\n" +
		"  --json은 명령 출력을 data.exec에 담아 한 번에 출력하며 대화형 터미널을 열지 않습니다."

	msgStageSelect = "인스턴스 선택"
	msgStageAttach = "명령 실행"

	msgFlagIndex = "다중 인스턴스 서비스에서 실행할 컨테이너 인덱스 (0부터)."
	msgFlagNoTTY = "대화형 터미널을 연결하지 않습니다 (docker compose exec -T)."

	msgNoBackend       = "스택 조회 또는 명령 실행 연결이 준비되지 않았습니다."
	msgNoBackendAction = "dcraft를 다시 설치하거나 빌드한 뒤 실행하세요."
	msgNoPayload       = "명령 결과를 표시할 수 없습니다."

	msgCommandRequired          = "실행할 명령이 필요합니다."
	msgCommandRequiredActionOne = "dcraft exec -s <서비스> -- <명령> [인자...]"
	msgCommandRequiredActionTwo = "dcraft는 셸을 대신 실행하지 않습니다. 이미지에 있는 명령과 인자를 그대로 지정하세요."

	msgInvalidIndex          = "인스턴스 인덱스가 올바르지 않습니다: %s"
	msgInvalidIndexActionOne = "인스턴스 인덱스는 0부터 시작하는 정수입니다. 예: -i 0"
	msgInvalidIndexActionTwo = "dcraft status로 현재 인스턴스 목록을 확인하세요."

	msgTerminalRestored  = "접속을 종료하고 터미널을 복원했습니다. 서비스는 계속 실행 중입니다."
	msgCanceled          = "접속을 종료했습니다. 터미널을 복원했고 서비스는 계속 실행 중입니다."
	msgCanceledActionOne = "dcraft status로 서비스가 계속 실행 중인지 확인하세요."
	msgCanceledActionTwo = "다시 접속하려면 dcraft exec를 실행하세요."

	msgNotStarted          = "명령을 시작하지 못했습니다."
	msgNotStartedActionOne = "dcraft status로 서비스 상태와 Docker 연결을 확인하세요."
	msgNotStartedActionTwo = "dcraft를 다시 설치하거나 빌드한 뒤 실행하세요."

	msgRemoteFailed          = "대상 컨테이너의 명령이 종료 코드 %d로 실패했습니다."
	msgRemoteFailedActionOne = "명령과 인자가 정확한지, 이미지에 해당 명령이 있는지 확인하세요."
	msgRemoteFailedActionTwo = "dcraft status로 서비스가 계속 실행 중인지 확인하세요."

	msgCommandMissing          = "대상 컨테이너에 명령이 없습니다: %s"
	msgCommandMissingActionOne = "이미지에 해당 명령이 포함되어 있는지 확인하세요. dcraft는 셸을 대신 실행하지 않습니다."
	msgCommandMissingActionTwo = "명령의 전체 경로를 지정하거나 이미지에 필요한 도구를 추가한 뒤 다시 실행하세요."

	msgServiceStopped          = "서비스가 실행 중이 아닙니다: %s"
	msgServiceStoppedActionOne = "dcraft up으로 스택을 시작한 뒤 다시 시도하세요."
	msgServiceStoppedActionTwo = "dcraft status로 현재 서비스 상태를 확인하세요."

	msgNothingRunning          = "실행 중인 서비스 인스턴스가 없습니다."
	msgNothingRunningActionOne = "dcraft up으로 스택을 시작한 뒤 다시 시도하세요."
	msgNothingRunningActionTwo = "dcraft status로 현재 상태를 확인하세요."

	msgFieldInstance        = "서비스 인스턴스"
	msgFieldInstanceHelp    = "실행 중인 인스턴스 선택 · 예: nginx-1 · node-api-1 · postgres-1"
	msgFieldCommand         = "명령"
	msgFieldCommandHelp     = "이미지 안의 명령과 인자 · 예: sh · node --version · psql -U postgres (셸 확장 없음, 따옴표로 인자 묶기 가능)"
	msgPickInstance         = "접속할 서비스 인스턴스를 선택하세요."
	msgPickInstanceAction   = "dcraft status로 실행 중인 인스턴스를 확인하세요."
	msgInvalidCommand       = "명령 형식이 올바르지 않습니다."
	msgInvalidCommandAction = "따옴표를 닫고 다시 입력하세요. 예: printf '%s' hello"

	msgFinished        = "명령을 실행했습니다."
	msgServiceLabel    = "서비스"
	msgCommandLabel    = "명령"
	msgExitLabel       = "종료 코드"
	msgServicesRunning = "서비스는 계속 실행 중입니다."
)
