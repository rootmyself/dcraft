package core

// Korean user-facing messages owned by the shared operation seam.
const (
	msgStageResolveTarget   = "대상 확인"
	msgStagePreflight       = "환경 확인"
	msgStageExecute         = "작업 실행"
	msgStageVerify          = "결과 확인"
	msgStageHistory         = "이력 기록"
	msgStagePlan            = "작업 계획"
	msgStageHooksPre        = "사전 스크립트"
	msgStageRevalidateInput = "입력 재확인"
	msgStageHooksPost       = "사후 스크립트"
	msgStageInputs          = "입력 확인"
	msgStageServiceCatalog  = "서비스 목록 확인"

	msgWorkingDirUnknown = "현재 작업 폴더를 확인할 수 없습니다."
	msgWorkingDirAction  = "터미널을 다시 열거나 --path로 대상 폴더를 지정하세요."

	msgApprovalRequiredActionOne = "대화형 화면에서 승인하거나, 자동화에서는 해당 승인 옵션을 명시하세요."
	msgApprovalRequiredActionTwo = "승인 없이는 작업을 실행하지 않습니다."

	msgApprovalInstallActionOne   = "변경 내용: 공식 Compose 배포물 1개를 현재 사용자의 Docker CLI 플러그인 경로에 추가합니다. Docker CLI·Engine·Desktop은 변경하지 않으며 관리자 권한을 요청하지 않습니다."
	msgApprovalInstallActionTwo   = "자동화에서 설치하려면 --approve install을 명시하세요. 실행 직후 출처·버전·설치 경로를 먼저 표시합니다."
	msgApprovalInstallActionThree = "대화형 화면에서는 설치 계획을 확인한 뒤 승인할 수 있습니다."

	msgPreflightFailed       = "환경 확인을 완료하지 못했습니다."
	msgPreflightFailedAction = "dcraft status로 Docker와 Compose 상태를 확인하세요."

	msgVerificationFailed          = "작업 결과를 확인하지 못했습니다."
	msgVerificationFailedActionOne = "healthcheck 결과와 컨테이너 상태를 확인하세요."
	msgVerificationFailedActionTwo = "dcraft status로 현재 상태를 확인하세요."

	msgHistoryFailed       = "작업 이력을 기록하지 못했습니다."
	msgHistoryFailedAction = "상태 폴더(DCRAFT_STATE_DIR)의 쓰기 권한과 여유 공간을 확인하세요."

	msgIdentityUnreadable          = "저장된 프로젝트 이름을 확인하지 못해 Compose 기본 이름으로 계속합니다."
	msgIdentityUnreadableActionOne = "상태 폴더(DCRAFT_STATE_DIR)의 names.json 권한과 JSON 내용을 확인하세요."
	msgIdentityUnreadableActionTwo = "dcraft는 읽기만 시도했으며 이 작업에서 상태를 쓰지 않았습니다."

	msgCanceled          = "작업이 중단되었습니다. 이미 완료된 단계의 효과는 남아 있을 수 있습니다."
	msgCanceledActionOne = "dcraft status로 현재 상태를 확인하세요."
	msgCanceledActionTwo = "중단된 작업을 성공으로 간주하지 마세요."

	msgInternalError       = "예상하지 못한 오류가 발생했습니다."
	msgInternalErrorAction = "dcraft status로 다시 확인하고, 같은 문제가 반복되면 출력 내용을 보관하세요."

	msgPlanUnsupported       = "이 작업은 실행 전에 확인할 계획을 제공하지 않습니다."
	msgPlanUnsupportedAction = "대화형 화면 없이 승인 옵션을 명시해 실행하거나 dcraft --help로 사용법을 확인하세요."

	msgPlanStale          = "승인한 계획과 현재 대상이 달라 실행하지 않았습니다."
	msgPlanStaleDetail    = "계획을 표시한 뒤 Compose 파일, 환경 파일, .dcraft.yaml, 프로젝트 이름 또는 이름 장부가 바뀌었습니다."
	msgPlanStaleActionOne = "명령을 다시 실행해 최신 미리보기를 확인한 뒤 승인하세요."
	msgPlanStaleActionTwo = "변경 중인 파일이 있다면 먼저 저장한 뒤 다시 미리보기하세요."

	msgInputsUnsupported       = "이 작업은 대화형 입력을 제공하지 않습니다."
	msgInputsUnsupportedAction = "명령과 옵션을 지정해 실행하거나 dcraft --help로 사용법을 확인하세요."

	msgServicesUnsupported             = "이 작업은 서비스 선택을 지원하지 않습니다."
	msgServicesUnsupportedAction       = "dcraft --help로 사용법을 확인하세요."
	msgServiceCatalogUnavailable       = "현재 스택의 서비스를 조회할 수 없습니다. 아무것도 실행하지 않았습니다."
	msgServiceCatalogUnavailableAction = "dcraft status로 스택 상태를 확인한 뒤 다시 시도하세요."
)

func approvalRequiredMessage(kind ApprovalKind) string {
	if kind == ApprovalInstall {
		return "Compose 설치에는 별도 승인이 필요합니다. 승인 없이는 내려받거나 파일을 만들지 않습니다."
	}
	return "이 작업에는 " + kind.KoreanLabel() + " 승인이 필요합니다."
}

func approvalRequiredActions(kind ApprovalKind) []string {
	switch kind {
	case ApprovalInstall:
		return []string{
			msgApprovalInstallActionOne,
			msgApprovalInstallActionTwo,
			msgApprovalInstallActionThree,
		}
	case ApprovalFileOverwrite:
		return []string{
			"기존 파일을 덮어쓰려면 --output <기존 파일>과 --approve file_overwrite를 함께 지정하세요.",
			"미리보기 후 대상 파일이 생기거나 바뀌었다면 다시 미리보기하세요. 승인 없이는 덮어쓰지 않습니다.",
			"기존 파일은 변경하지 않았습니다.",
		}
	}
	return []string{msgApprovalRequiredActionOne, msgApprovalRequiredActionTwo}
}
