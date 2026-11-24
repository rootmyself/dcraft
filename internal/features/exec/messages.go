package exec

const (
	// Input field keys consumed by WithInputs and rendered by the TUI.
	fieldInstance      = "instance"
	fieldCommand       = "command"
	fieldCommandChoice = "command-choice"
)

// Korean user-facing text owned by the exec feature.
const (
	msgTitle   = "서비스 접속"
	msgSummary = "실행 중인 서비스 인스턴스에서 지정한 명령을 정확히 실행."
	msgUsage   = "dcraft exec -s <서비스> [-i <인스턴스>] [-T] -- <명령> [인자...]\n" +
		"  -s/--service와 명령은 필수. dcraft는 셸을 대신 실행하지 않으므로\n" +
		"  이미지 안에 해당 명령 필요.\n" +
		"  대화형 터미널은 입력·출력을 연결. -T 또는 비대화형은 연결하지 않음.\n" +
		"  --json은 명령 출력을 data.exec에 담아 한 번에 출력. 대화형 터미널은 열지 않음."

	msgStageSelect = "인스턴스 선택"
	msgStageAttach = "명령 실행"

	msgFlagIndex = "다중 인스턴스 서비스에서 실행할 컨테이너 인덱스 (0부터)."
	msgFlagNoTTY = "대화형 터미널을 연결하지 않음 (docker compose exec -T)."

	msgNoBackend       = "스택 조회 또는 명령 실행 연결이 준비되지 않았음."
	msgNoBackendAction = "dcraft를 다시 설치하거나 빌드한 뒤 실행."
	msgNoPayload       = "명령 결과 표시 불가."

	msgCommandRequired          = "실행할 명령이 필요."
	msgCommandRequiredActionOne = "dcraft exec -s <서비스> -- <명령> [인자...]"
	msgCommandRequiredActionTwo = "dcraft는 셸을 대신 실행하지 않음. 이미지에 있는 명령과 인자를 그대로 지정."

	msgInvalidIndex          = "인스턴스 인덱스가 올바르지 않음: %s"
	msgInvalidIndexActionOne = "인스턴스 인덱스는 0부터 시작하는 정수. 예: -i 0"
	msgInvalidIndexActionTwo = "dcraft status로 현재 인스턴스 목록 확인."

	msgTerminalRestored  = "접속을 종료하고 터미널 복원. 서비스는 계속 실행 중."
	msgCanceled          = "접속을 종료. 터미널을 복원했고 서비스는 계속 실행 중."
	msgCanceledActionOne = "dcraft status로 서비스가 계속 실행 중인지 확인."
	msgCanceledActionTwo = "다시 접속하려면 dcraft exec를 실행."

	msgNotStarted          = "명령 시작 실패."
	msgNotStartedActionOne = "dcraft status로 서비스 상태와 Docker 연결 확인."
	msgNotStartedActionTwo = "dcraft를 다시 설치하거나 빌드한 뒤 실행."

	msgRemoteFailed          = "대상 컨테이너의 명령이 종료 코드 %d로 실패."
	msgRemoteFailedActionOne = "명령과 인자가 정확한지, 이미지에 해당 명령이 있는지 확인."
	msgRemoteFailedActionTwo = "dcraft status로 서비스가 계속 실행 중인지 확인."

	msgCommandMissing          = "대상 컨테이너에 명령이 없음: %s"
	msgCommandMissingActionOne = "이미지에 해당 명령이 포함되어 있는지 확인. dcraft는 셸을 대신 실행하지 않음."
	msgCommandMissingActionTwo = "명령의 전체 경로를 지정하거나 이미지에 필요한 도구를 추가한 뒤 다시 실행."

	msgServiceStopped          = "서비스가 실행 중이 아님: %s"
	msgServiceStoppedActionOne = "dcraft up으로 스택을 시작한 뒤 다시 시도."
	msgServiceStoppedActionTwo = "dcraft status로 현재 서비스 상태 확인."

	msgNothingRunning          = "실행 중인 서비스 인스턴스가 없음."
	msgNothingRunningActionOne = "dcraft up으로 스택을 시작한 뒤 다시 시도."
	msgNothingRunningActionTwo = "dcraft status로 현재 상태 확인."

	msgShellDefault      = " (기본)"
	msgShellFallback     = " (bash가 없을 때)"
	msgRecentPrefix      = "최근: "
	msgNewCommand        = "새 명령 직접 입력"
	msgHistoryUnreadable = "최근 명령 기록을 읽지 못했음. 이번 선택은 기록하지 않음."
	msgHistoryIgnore     = "최근 명령은 %s에 저장됨. git에 올리지 않으려면 .gitignore에 그 경로를 추가."

	msgFieldCommandChoice     = "명령 선택"
	msgFieldCommandChoiceHelp = "기본 셸 또는 이 프로젝트에서 최근 실행한 명령 선택 · 삭제는 선택 화면에서"
	msgFieldInstance          = "서비스 인스턴스"
	msgFieldInstanceHelp      = "실행 중인 인스턴스 선택 · 예: nginx-1 · node-api-1 · postgres-1"
	msgFieldCommand           = "명령"
	msgFieldCommandHelp       = "이미지 안의 명령과 인자 · 예: sh · node --version · psql -U postgres (셸 확장 없음, 따옴표로 인자 묶기 가능)"
	msgPickInstance           = "접속할 서비스 인스턴스 선택."
	msgPickInstanceAction     = "dcraft status로 실행 중인 인스턴스 확인."
	msgInvalidCommand         = "명령 형식이 올바르지 않음."
	msgInvalidCommandAction   = "따옴표를 닫고 다시 입력. 예: printf '%s' hello"

	msgFinished        = "명령 실행 완료."
	msgSubstituted     = "이미지에 bash가 없어 /bin/sh로 대체 실행."
	msgServiceLabel    = "서비스"
	msgCommandLabel    = "명령"
	msgExitLabel       = "종료 코드"
	msgServicesRunning = "서비스는 계속 실행 중."
)

// The attach screen warns when the typed command looks like it carries a
// credential. dcraft never inspects the value; it only names the risk so a
// command that would be stored in the project folder is not stored with a
// secret by accident.
const msgCredentialRisk = "명령에 비밀값이 들어 있는 것으로 보임. 그대로 실행은 가능하지만 최근 명령에 저장되므로 값 노출 확인."
