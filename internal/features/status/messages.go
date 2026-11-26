package status

// Korean user-facing text owned by the status feature.
const (
	msgTitle   = "서비스 상태"
	msgSummary = "현재 폴더 서비스의 상태와 연결 정보 확인."
	msgUsage   = "dcraft status [--path <폴더>] [--json]"

	msgStageInspect    = "스택 조회"
	msgNoBackend       = "스택 조회 연결이 준비되지 않았음."
	msgNoBackendAction = "dcraft를 다시 설치하거나 빌드한 뒤 실행."
	msgNoPayload       = "상태 정보 표시 불가."
	msgNoServices      = "서비스가 없음. Compose 파일에 services 항목이 있는지 확인."
	msgUnknownProject  = "(프로젝트 이름 확인 중)"
	msgNone            = "없음"
	msgLocalConnection = "로컬 연결"

	msgNoInstances           = "인스턴스: 없음"
	msgUnconfigured          = "현재 Compose 설정에 없음 (다른 프로필 또는 이전 설정의 컨테이너)"
	msgLocalBuild            = "(로컬 빌드)"
	msgAllInterfaces         = "모든 인터페이스"
	msgDynamicPort           = "동적 호스트 포트"
	msgConfiguredPort        = " (설정값)"
	msgNoHealthcheck         = "healthcheck 없음 · 앱 확인 안 됨"
	msgHealthcheckConfigured = "healthcheck 설정됨"
	msgHealthMissing         = "health 확인 정보 없음 · 앱 확인 안 됨"
	msgHealthStarting        = "health 확인 중 · 앱 확인 안 됨"
	msgHealthyVerified       = "healthy · 앱 확인됨"
	msgHealthyUnverified     = "healthy · 앱 확인 안 됨"
	msgHealthUnhealthy       = "unhealthy · 앱 확인 안 됨"

	msgGuideRefreshInteractive = "상태를 새로 고치려면: dcraft status · 대화형 화면을 열려면: dcraft"
	msgGuidePending            = "관리 작업: 갱신은 dcraft update, 로그는 dcraft logs, 실행·중지·제거·재시작은 dcraft up/stop/down/restart, 데이터 백업은 dcraft backup, 데이터 복원은 dcraft restore를 사용."
	msgGuideSecrets            = "기존 Compose/.env의 민감값은 자동 제거되지 않음. dcraft secrets로 참조 등록."

	// State and health wording comes from internal/stack/present, the single
	// mapping the TUI surfaces share; this file keeps only the status
	// document's own labels. The summary badges are the document's counts, not
	// the CLI's sentence form.
	msgBadgeServices   = "서비스 %d"
	msgBadgeRunning    = "실행 중 %d"
	msgBadgeInstances  = "인스턴스 %d"
	msgBadgeUnhealthy  = "비정상 %d"
	msgBadgeUnverified = "앱 확인 안 됨 %d"
	msgBadgeStopped    = "중지 %d"
	msgBadgeNotCreated = "미생성 %d"

	msgPairObserved    = "마지막 조회"
	msgPairComposeFile = "Compose 파일"
	msgPairProject     = "프로젝트"
	msgColumnService   = "서비스"
	msgColumnState     = "상태"
	msgColumnInstances = "인스턴스"
	msgColumnHealth    = "health"
	msgColumnImage     = "이미지"
	msgColumnPort      = "포트"
	msgEmptyStack      = "서비스가 없음. Compose 파일에 services 항목이 있는지 확인."
	msgNoPort          = "-"

	msgEmptyBadge = "서비스 없음"
)
