package status

// Korean user-facing text owned by the status feature.
const (
	msgTitle   = "스택 상태"
	msgSummary = "현재 폴더 스택의 서비스 상태와 연결 정보를 확인합니다."
	msgUsage   = "dcraft status [--path <폴더>] [--json]"

	msgStageInspect    = "스택 조회"
	msgNoBackend       = "스택 조회 연결이 준비되지 않았습니다."
	msgNoBackendAction = "dcraft를 다시 설치하거나 빌드한 뒤 실행하세요."
	msgNoPayload       = "상태 정보를 표시할 수 없습니다."
	msgNoServices      = "서비스가 없습니다. Compose 파일에 services 항목이 있는지 확인하세요."
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

	msgGuideRefresh     = "상태를 새로 고치려면: dcraft status"
	msgGuideInteractive = "대화형 화면을 열려면: dcraft"
	msgGuidePending     = "관리 작업 중 갱신·로그·백업은 아직 제공되지 않습니다. 실행·중지·제거·재시작은 dcraft up/stop/down/restart를 사용하세요."
)
