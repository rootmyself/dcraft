package logs

// Korean user-facing text owned by the logs feature.
const (
	msgTitle   = "로그 보기"
	msgSummary = "스택 또는 서비스의 로그를 실시간으로 따라보거나 마지막 로그를 읽습니다."
	msgUsage   = "dcraft logs [-s <서비스>] [-i <인스턴스>] [--no-follow] [--tail <줄 수>] [--since <시각>] [--timestamps]\n" +
		"  기본은 실시간 따라보기입니다. ctrl+c로 화면만 분리하며 서비스는 중지하지 않습니다.\n" +
		"  --json은 실시간 로그를 하나의 결과로 표현할 수 없어 지원하지 않습니다."

	msgStageSelect = "인스턴스 선택"
	msgStageStream = "로그 스트림"

	msgFlagNoFollow   = "실시간 따라보기를 끄고 현재까지의 로그만 읽습니다."
	msgFlagTail       = "처음에 표시할 로그 줄 수 (예: 100). 기본은 Compose 전체입니다."
	msgFlagSince      = "지정한 시각 이후의 로그만 표시합니다 (예: 10m, 2026-11-18T10:00:00)."
	msgFlagTimestamps = "각 로그 줄에 시각을 표시합니다."
	msgFlagIndex      = "다중 인스턴스 서비스에서 읽을 컨테이너 인덱스 (0부터, -s 서비스 하나와 함께)."

	msgNoBackend       = "스택 조회 또는 로그 스트림 연결이 준비되지 않았습니다."
	msgNoBackendAction = "dcraft를 다시 설치하거나 빌드한 뒤 실행하세요."
	msgNoPayload       = "로그 결과를 표시할 수 없습니다."

	msgInvalidIndex          = "인스턴스 인덱스가 올바르지 않습니다: %s"
	msgInvalidIndexActionOne = "인스턴스 인덱스는 0부터 시작하는 정수입니다. 예: -i 0"
	msgInvalidIndexActionTwo = "dcraft status로 현재 인스턴스 목록을 확인하세요."

	msgStreamDetached    = "로그 스트림을 분리했습니다. 서비스는 계속 실행 중입니다."
	msgCanceled          = "로그 따라보기를 중단했습니다. 로그 스트림만 분리되었고 서비스는 계속 실행 중입니다."
	msgCanceledActionOne = "dcraft status로 서비스가 계속 실행 중인지 확인하세요."
	msgCanceledActionTwo = "다시 보려면 dcraft logs를 실행하세요."

	msgFailure          = "로그를 읽지 못했습니다."
	msgFailureActionOne = "dcraft status로 서비스와 컨테이너 상태를 확인하세요."
	msgFailureActionTwo = "서비스가 시작된 뒤 dcraft logs를 다시 실행하세요."

	msgFinished     = "로그 읽기를 마쳤습니다. 서비스는 계속 실행 중입니다."
	msgWholeStack   = "스택 전체"
	msgTargetLabel  = "대상"
	msgNoFollowDone = "--no-follow: 지금까지의 로그만 읽었습니다."
)
