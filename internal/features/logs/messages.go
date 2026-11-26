package logs

// Korean user-facing text owned by the logs feature.
const (
	msgTitle   = "서비스 로그"
	msgSummary = "선택한 서비스의 로그를 실시간으로 따라보거나 마지막 로그를 읽음."
	msgUsage   = "dcraft logs -s <서비스> [-i <인스턴스>] [--no-follow] [--tail <줄 수>] [--since <시각>] [--timestamps]\n" +
		"  -s/--service는 필수이며 여러 번 지정 가능. 전체 스택 로그는 지원하지 않음.\n" +
		"  기본은 실시간 따라보기. ctrl+c로 화면만 분리하며 서비스는 중지하지 않음.\n" +
		"  대화형 화면에서는 최대 4개 서비스를 탭으로 함께 보여 줌.\n" +
		"  --json은 실시간 로그를 하나의 결과로 표현할 수 없어 지원하지 않음."

	msgStageSelect = "인스턴스 선택"
	msgStageStream = "로그 스트림"

	msgServiceRequired          = "로그를 볼 서비스 하나 이상 선택 필요."
	msgServiceRequiredActionOne = "dcraft logs -s <서비스> [-s <서비스> ...]"
	msgServiceRequiredActionTwo = "서비스 이름은 dcraft status에서 확인."

	msgFlagNoFollow   = "실시간 따라보기를 끄고 현재까지의 로그만 읽음."
	msgFlagTail       = "처음에 표시할 로그 줄 수 (예: 100). 기본은 Compose 전체."
	msgFlagSince      = "지정한 시각 이후의 로그만 표시 (예: 10m, 2026-11-18T10:00:00)."
	msgFlagTimestamps = "각 로그 줄에 시각 표시."
	msgFlagIndex      = "다중 인스턴스 서비스에서 읽을 컨테이너 인덱스 (0부터, -s 서비스 하나와 함께)."

	msgNoBackend       = "스택 조회 또는 로그 스트림 연결이 준비되지 않았음."
	msgNoBackendAction = "dcraft를 다시 설치하거나 빌드한 뒤 실행."
	msgNoPayload       = "로그 결과 표시 불가."

	msgInvalidIndex          = "인스턴스 인덱스가 올바르지 않음: %s"
	msgInvalidIndexActionOne = "인스턴스 인덱스는 0부터 시작하는 정수. 예: -i 0"
	msgInvalidIndexActionTwo = "dcraft status로 현재 인스턴스 목록 확인."

	msgStreamDetached    = "로그 스트림 분리. 서비스는 계속 실행 중."
	msgCanceled          = "로그 따라보기 중단. 로그 스트림만 분리되었고 서비스는 계속 실행 중."
	msgCanceledActionOne = "dcraft status로 서비스가 계속 실행 중인지 확인."
	msgCanceledActionTwo = "다시 보려면 dcraft logs를 실행."

	msgFailure          = "로그 읽지 못함."
	msgFailureActionOne = "dcraft status로 서비스와 컨테이너 상태 확인."
	msgFailureActionTwo = "서비스가 시작된 뒤 dcraft logs를 다시 실행."

	msgFinished     = "로그 읽기 완료. 서비스는 계속 실행 중."
	msgWholeStack   = "스택 전체"
	msgTargetLabel  = "대상"
	msgNoFollowDone = "--no-follow: 지금까지의 로그만 읽었음."
)
