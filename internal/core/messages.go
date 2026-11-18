package core

// Korean user-facing messages owned by the shared operation seam.
const (
	msgStageResolveTarget = "대상 확인"
	msgStagePreflight     = "환경 확인"
	msgStageExecute       = "작업 실행"
	msgStageVerify        = "결과 확인"
	msgStageHistory       = "이력 기록"

	msgWorkingDirUnknown = "현재 작업 폴더를 확인할 수 없습니다."
	msgWorkingDirAction  = "터미널을 다시 열거나 --path로 대상 폴더를 지정하세요."

	msgApprovalRequiredActionOne = "대화형 화면에서 승인하거나, 자동화에서는 해당 승인 옵션을 명시하세요."
	msgApprovalRequiredActionTwo = "승인 없이는 작업을 실행하지 않습니다."

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
)

func approvalRequiredMessage(kind ApprovalKind) string {
	return "이 작업에는 " + kind.KoreanLabel() + " 승인이 필요합니다."
}
