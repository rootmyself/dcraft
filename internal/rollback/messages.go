package rollback

// Korean user-facing text owned by the rollback feature. Command names, flag
// values and machine categories stay English.
const (
	msgStageLoadRecord = "기록 확인"
	msgStagePrecheck   = "변경 전 확인"
	msgStageApply      = "이전 이미지 적용"
	msgStageVerify     = "실행 상태 확인"
	msgStageReport     = "결과 정리"

	msgNoController    = "이전 버전 복원 컨트롤러가 준비되지 않았습니다."
	msgNoControllerAct = "dcraft를 다시 빌드하거나 설치한 뒤 다시 실행하세요."
	msgNoState         = "이전 버전 기록을 읽을 상태 저장소가 준비되지 않았습니다."
	msgNoStateAct      = "DCRAFT_STATE_DIR 설정과 상태 폴더 권한을 확인하세요."

	msgNoRecord     = "이 스택과 실행환경에는 기록된 정상 버전이 없습니다."
	msgNoRecordAct1 = "갱신(dcraft update)이 성공해야 그 결과가 정상 버전으로 기록됩니다. 실패한 갱신은 기록을 덮어쓰지 않습니다."
	msgNoRecordAct2 = "dcraft가 관리하기 전에 만들었거나 한 번도 갱신하지 않은 스택에는 돌아갈 과거 버전이 없으며, dcraft는 없는 과거 버전을 만들지 않습니다."
	msgNoRecordAct3 = "dcraft status로 현재 실행 상태를 확인하세요."

	msgRecordInvalid = "저장된 정상 버전 기록을 사용할 수 없습니다."
	msgRecordAct1    = "기록 파일(state.json)이 손상되었거나 다른 폴더·실행환경의 기록입니다."
	msgRecordAct2    = "기록을 임의로 고치지 말고 현재 상태를 확인한 뒤, 필요하면 dcraft update를 다시 실행해 정상 버전을 기록하세요."

	msgInputMissing = "기록된 실행 설정 파일을 찾지 못했습니다."
	msgInputAct1    = "기록에 저장된 Compose 파일·환경 파일·대상 폴더를 복원한 뒤 다시 실행하세요."
	msgInputAct2    = "파일이 모두 사라졌다면 dcraft는 이전 버전을 재현하지 않습니다. current 설정을 확인하고 dcraft update로 새 정상 버전을 기록하세요."

	msgImageMissing = "기록된 이전 이미지를 찾지 못했습니다."
	msgImageAct1    = "이전 이미지나 보존 태그(dcraft-good/...)가 삭제되었거나 다른 이미지로 이동했습니다."
	msgImageAct2    = "dcraft는 없는 이미지를 최신 이미지로 대신하지 않습니다. 이미지를 복원한 뒤 다시 실행하거나, 현재 상태를 확인하고 dcraft update로 새 정상 버전을 기록하세요."

	msgImageIDMissing  = "기록된 이미지 ID가 로컬에 없습니다: %s"
	msgImageTagMissing = "기록된 보존 태그가 없습니다: %s"
	msgImageTagMoved   = "기록된 보존 태그가 다른 이미지를 가리킵니다: %s (기록 %s, 실제 %s)"

	msgSecretUnavailable = "기록된 비밀값 원본을 다시 읽을 수 없습니다."
	msgSecretAct1        = "기록된 당시 참조의 환경변수·파일 원본이 있는지 확인하세요. 비밀값 자체는 출력하지 않습니다."
	msgSecretAct2        = "이전 비밀값의 재현은 보장되지 않습니다. 외부 원본을 복구한 뒤 다시 실행하거나, 현재 값으로 dcraft update를 실행하세요."

	msgApplyFailed  = "이전 이미지를 적용하지 못했습니다 (%s)."
	msgApplyAct1    = "완료된 단계와 현재 컨테이너 상태를 확인하세요. 부분 반영이 남아 있을 수 있습니다."
	msgApplyAct2    = "dcraft status로 현재 상태를 확인하고, 필요하면 dcraft rollback을 다시 실행하세요."
	msgApplyAct3    = "같은 기록으로 다시 실행하면 같은 이미지를 유지합니다. dcraft는 실패한 복원을 성공으로 기록하지 않습니다."
	msgStateUnknown = "현재 컨테이너 상태를 읽지 못했습니다."

	msgVerifyImageMismatch = "복원 결과의 이미지가 기록과 다릅니다."
	msgVerifyImageDetail   = "%s: 기록 %s, 실제 %s"
	msgVerifyAct1          = "컨테이너를 다시 만드는 동안 외부에서 이미지가 바뀌지 않았는지 확인하세요."
	msgVerifyAct2          = "dcraft status로 실제 실행 이미지를 확인하고, 같은 문제가 반복되면 기록된 보존 태그와 이미지를 점검하세요."

	msgReadinessFailed  = "복원 후 실행 상태가 정상으로 확인되지 않았습니다: %s"
	msgReadinessTimeout = "%d초 안에 복원 후 실행 상태가 정상으로 확인되지 않았습니다: %s"
	msgReadinessAct1    = "dcraft logs로 대상 서비스의 로그를 확인하세요."
	msgReadinessAct2    = "healthcheck 설정과 종료 코드를 확인하세요. unhealthy·종료·재시작 반복·시간 초과는 성공이 아닙니다."
	msgReadinessAct3    = "필요하면 환경을 고친 뒤 dcraft rollback을 다시 실행하세요. dcraft는 자동으로 다시 되돌리지 않습니다."
	msgHealthStarting   = "healthcheck가 아직 끝나지 않았습니다."
	msgStateNoContainer = "대상 서비스의 컨테이너가 없습니다."
	msgStateRestarting  = "컨테이너가 다시 시작되는 중입니다."

	msgStackBusy     = "같은 스택에서 다른 작업이 진행 중입니다."
	msgStackBusyAct1 = "진행 중인 작업이 끝난 뒤 다시 실행하세요."
	msgStackBusyAct2 = "dcraft status로 현재 상태를 확인하세요."

	msgStateFailed = "상태 저장소를 읽거나 잠그지 못했습니다."
	msgStateAct1   = "상태 폴더(DCRAFT_STATE_DIR)의 권한과 여유 공간을 확인하세요."
	msgStateAct2   = "dcraft 상태를 다른 도구가 동시에 고치고 있지 않은지 확인하세요."

	msgInvalidTimeout    = "이전 버전 복원의 --timeout 값이 올바르지 않습니다: %s (정수 초, 1~3600)"
	msgInvalidTimeoutAct = "--timeout에 1~3600 사이의 정수 초를 지정하세요. 이 값은 복원 후 상태 확인 시간만 제한합니다."

	msgProjectNameConflict    = "이전 버전 복원은 기록된 프로젝트 이름으로만 실행합니다."
	msgProjectNameConflictAct = "rollback에서 -p/--project-name을 빼고 다시 실행하세요. 기록된 이름과 다른 이름을 지정하면 다른 스택을 조작할 수 있어 실행하지 않습니다."

	msgPlanHeader           = "이전 정상 버전 복원 계획"
	msgSuccessFolder        = "대상 폴더"
	msgSuccessFiles         = "Compose 파일"
	msgSuccessEnvFiles      = "환경 파일"
	msgSuccessProfiles      = "프로필"
	msgSuccessProject       = "프로젝트 이름"
	msgSuccessEnv           = "실행환경"
	msgSuccessRecorded      = "기록 시각"
	msgSuccessServices      = "대상 서비스"
	msgPlanImages           = "복원 이미지"
	msgPlanApply            = "적용 방법"
	msgPlanApplyValue       = "기록된 이미지 ID로 고정하고 pull·build 없이 다시 적용합니다."
	msgPlanApproval         = "승인"
	msgPlanInterrupt        = "적용 중 대상 서비스의 컨테이너가 다시 만들어지는 동안 짧은 중단이 있을 수 있습니다."
	msgPlanNotRestored      = "되돌리지 않는 것"
	msgPlanNotRestoredValue = "DB·볼륨 데이터, 외부 시스템, 배포 훅과 스크립트 부작용은 복원하지 않습니다. 훅은 다시 실행하지 않습니다."
	msgPlanRecordNote       = "이 기록은 유지됩니다. 이전 정상 버전은 하나만 유지되므로 더 오래된 버전은 없습니다. 같은 기록으로 다시 복원하면 같은 이미지를 유지합니다."
	msgPlanRepeatNote       = "이미 이 기록의 이미지가 실행 중이면 컨테이너를 다시 만들지 않습니다."
	msgPlanDisclaimer       = "이 계획은 실행 전 단계 설명이며 정확한 미래 상태 diff가 아닙니다. 외부 이미지와 사용자 파일은 실행 시점에 달라질 수 있습니다."
	msgPlanExtra            = "기록에 없는 서비스는 이번 복원 대상이 아니며 건드리지 않습니다: %s"
	msgPlanOrphans          = "기록에 없는 서비스의 컨테이너는 그대로 둡니다: %s"

	msgSuccessHeader     = "이전 정상 버전 복원 완료"
	msgSuccessUnverified = "이전 정상 이미지 적용 완료 · 앱 상태 확인 안 됨"
	msgSuccessWork       = "적용 결과"
	msgSuccessReadiness  = "실행 상태"
	msgSuccessRecord     = "정상 버전 기록"
	msgSuccessRecordKept = "기록을 그대로 유지했습니다(변경 없음). 더 오래된 버전은 없습니다."
	msgSuccessNone       = "없음"
	msgSuccessNext       = "다음 작업"
	msgSuccessNextStatus = "dcraft status로 실행 상태를 확인하세요."
	msgSuccessNextUpdate = "현재 설정으로 새 정상 버전을 기록하려면 dcraft update를 실행하세요."
	msgSuccessNextData   = "데이터를 되돌리려면 별도의 데이터 복원(백업)을 사용하세요. 이 복원은 DB·볼륨 데이터를 되돌리지 않았습니다."
	msgSuccessAlready    = "이미 기록된 이미지가 실행 중이어서 컨테이너를 다시 만들지 않았습니다."
)
