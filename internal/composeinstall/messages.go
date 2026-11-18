package composeinstall

// Korean user-facing text owned by the Compose install feature. Commands,
// flags, JSON keys and machine values stay English.
const (
	msgTitle   = "Compose 설치"
	msgSummary = "누락된 Docker Compose를 공식 배포물로 설치하고 다시 확인합니다."
	msgUsage   = "dcraft install-compose [--approve install] [--json]"

	msgPlanTitle = "Compose 설치 계획"

	msgStagePlan     = "설치 계획"
	msgStageDownload = "내려받기"
	msgStageVerify   = "무결성 확인"
	msgStageInstall  = "설치"
	msgStageRecheck  = "기능 재확인"

	msgReleaseUnavailable   = "공식 Compose 릴리스 정보를 확인하지 못했습니다."
	msgReleaseAssetMissing  = "이 운영체제·아키텍처에 맞는 공식 Compose 배포물을 찾지 못했습니다."
	msgPlatformUnsupported  = "이 운영체제·아키텍처에는 안전한 자동 설치를 지원하지 않습니다."
	msgIntegrityUnavailable = "공식 릴리스의 무결성 정보를 확인할 수 없어 설치를 중단했습니다."
	msgIntegrityConflicting = "공식 릴리스의 무결성 정보가 서로 일치하지 않아 설치를 중단했습니다."

	msgDownloadFailed    = "Compose 배포물을 내려받지 못했습니다."
	msgChecksumMismatch  = "내려받은 파일의 SHA-256이 공식 체크섬과 다릅니다."
	msgArtifactInvalid   = "내려받은 파일이 이 운영체제에서 실행할 수 있는 형식이 아닙니다."
	msgArtifactArch      = "내려받은 파일의 아키텍처가 이 컴퓨터와 다릅니다."
	msgInstallExists     = "대상 경로에 이미 Compose 관련 파일이 있어 dcraft는 덮어쓰지 않습니다."
	msgInstallPermission = "Compose를 설치할 경로에 쓸 권한이 없습니다."
	msgInstallFailed     = "Compose 파일을 설치하지 못했습니다."
	msgRecheckFailed     = "Compose 파일은 설치되었지만 docker compose version 확인에 실패했습니다."
	msgAlreadyAvailable  = "Docker Compose가 이미 사용 가능하여 아무것도 변경하지 않았습니다."
	msgNoDockerCLI       = "Docker CLI를 찾을 수 없어 Compose 플러그인을 설치할 수 없습니다."

	msgPlanSourceLabel = "출처"
	msgPlanVersion     = "버전"
	msgPlanTarget      = "대상 경로"
	msgPlanPermission  = "필요한 권한"
	msgPlanEffect      = "변경 내용"
	msgPlanIntegrity   = "무결성"
	msgPlanNoChange    = "변경 내용: 없음 (이미 사용 가능)"

	msgPermissionUserArea = "현재 사용자 계정의 사용자 영역에 파일 1개를 만듭니다. 관리자 권한(sudo)은 필요하지 않습니다."
	msgEffectInstall      = "공식 Compose 플러그인 파일 1개를 추가합니다. Docker CLI·Engine·Desktop과 WSL 통합은 변경하지 않습니다."
	msgIntegritySHA256    = "공식 릴리스가 게시한 SHA-256 체크섬과 파일 크기를 확인한 뒤 설치합니다."

	msgStatusInstalled = "설치됨"
	msgStatusUnchanged = "변경 없음"
	msgGuidanceOne     = "다음 행동: dcraft status 로 Compose 사용 가능 여부를 다시 확인하세요."
	msgGuidanceTwo     = "dcraft는 Docker 자체를 설치·교체하거나 관리자 권한을 올리지 않습니다."
)

func manualInstallActions(dir string) []string {
	if dir == "" {
		dir = "~/.docker/cli-plugins"
	}
	return []string{
		"공식 릴리스(https://github.com/docker/compose/releases)에서 운영체제와 아키텍처에 맞는 docker-compose 파일을 직접 내려받으세요.",
		"mkdir -p " + dir + " && mv <내려받은 파일> " + dir + "/docker-compose && chmod +x " + dir + "/docker-compose",
		"docker compose version 으로 설치를 확인하세요.",
		"dcraft는 관리자 권한(sudo) 상승이나 Docker 교체를 대신 수행하지 않습니다.",
	}
}

// Cancellation text specific to the atomic plugin install. The install is
// published with one rename-like operation, so the target is either absent or
// complete; this is what the user needs to know after an interruption.
const (
	msgInstallCanceled          = "설치가 중단되었습니다. 설치 대상에는 완전한 파일만 나타나며, 중단된 다운로드나 검증 단계의 임시 파일은 정리됩니다."
	msgInstallCanceledActionOne = "대상 경로에 docker-compose 파일이 없으면 설치되지 않은 것입니다. 있으면 설치가 완료된 뒤 중단된 것이므로 docker compose version으로 확인하세요."
	msgInstallCanceledActionTwo = "Docker CLI·Engine·Desktop은 이 작업으로 변경되지 않았습니다."
)
