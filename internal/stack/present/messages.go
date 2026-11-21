package present

// Korean user-facing labels for stack states and health. They are the same
// words every surface draws, so they live here once.
const (
	msgStateRunning    = "실행 중"
	msgStateExited     = "중지됨"
	msgStatePaused     = "일시정지됨"
	msgStateRestarting = "재시작 중"
	msgStateCreated    = "생성됨"
	msgStateNotCreated = "컨테이너 없음"
	msgStateUnknown    = "상태 알 수 없음"

	msgHealthHealthy   = "정상"
	msgHealthUnhealthy = "비정상"
	msgHealthStarting  = "확인 중"
	msgHealthNone      = "없음"
)
