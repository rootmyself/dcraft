package backup

// Korean user-facing text owned by the backup feature and its shared contract.
const (
	msgTitle   = "데이터 백업"
	msgSummary = "선택한 데이터 볼륨을 서비스를 잠시 멈춘 뒤 로컬 폴더에 백업합니다."
	msgUsage   = `dcraft backup --volume <볼륨 키> --output <폴더> [--path <스택 폴더>]

선택한 스택의 이름 있는 데이터 볼륨만 백업합니다. 이미지·로그·환경 변수·비밀 파일을
무차별로 모으지 않습니다. 소비 서비스를 확인해 도구가 중지한 서비스만 백업 후 다시
시작하며, 다른 스택의 컨테이너는 중지하지 않습니다.`

	msgNoBackend       = "백업 기능이 데이터 경계와 연결되지 않았습니다."
	msgNoBackendAction = "dcraft를 다시 빌드하거나 설치한 뒤 실행하세요."

	msgStageOutput        = "저장 위치 확인"
	msgStageCoordinate    = "스택 잠금"
	msgStageResolve       = "프로젝트 확인"
	msgStageDiscover      = "데이터 선택"
	msgStageRecheck       = "선택 재확인"
	msgStageConsumers     = "소비자 확인"
	msgStageStop          = "서비스 중지"
	msgStageArchive       = "볼륨 복사"
	msgStageVerifyArchive = "무결성 확인"
	msgStagePublish       = "백업 공개"
	msgStageRestoreState  = "서비스 상태 복구"
	msgStageComplete      = "완료 표시"

	msgOutputRequired       = "백업 저장 위치를 지정하지 않았습니다."
	msgOutputRequiredAction = "--output/-o로 백업을 저장할 폴더를 지정하세요. dcraft는 숨은 기본 위치를 선택하지 않습니다."
	msgOutputUsageAction    = "예: dcraft backup --volume data --output ./dcraft-backups"

	msgOutputInvalid       = "백업 저장 위치를 사용할 수 없습니다."
	msgOutputInvalidAction = "폴더 경로와 쓰기 권한, 남은 공간을 확인한 뒤 다시 실행하세요."

	msgVolumeRequired       = "백업할 데이터 볼륨을 지정하지 않았습니다."
	msgVolumeRequiredAction = "--volume으로 백업할 볼륨 키를 하나 이상 지정하세요."
	msgVolumeListAction     = "대화형 화면에서는 사용 가능한 볼륨을 직접 선택할 수 있습니다."

	msgVolumeUnknown       = "선택한 데이터 볼륨을 찾지 못했습니다."
	msgVolumeUnknownAction = "볼륨 키는 docker compose config의 volumes 키입니다. 컨테이너 경로가 아니라 키를 지정하세요."

	msgVolumeUnsupported       = "이 데이터 원본은 이 명령으로 백업할 수 없습니다."
	msgVolumeUnsupportedAction = "이름 있는 볼륨만 백업할 수 있습니다. 연결 폴더(bind mount)와 익명 볼륨은 별도 안내를 따르세요."

	msgSelectionChanged       = "선택한 데이터 원본이 확인 후 바뀌어 백업하지 않았습니다."
	msgSelectionChangedBefore = "확인한 선택: "
	msgSelectionChangedAfter  = "다시 확인한 선택: "
	msgSelectionChangedAction = "스택 구성을 바꾼 작업이 없는지 확인한 뒤 dcraft backup을 다시 실행하세요. dcraft는 바뀐 대상을 임의로 백업하지 않습니다."

	msgConsumerUnsafe       = "다른 스택의 컨테이너가 같은 볼륨을 사용하고 있어 일관된 백업을 만들 수 없습니다."
	msgConsumerUnsafeAction = "해당 컨테이너를 소유한 스택이나 사용자가 직접 중지한 뒤 다시 실행하세요."
	msgConsumerUnsafeNoStop = "dcraft는 다른 스택의 컨테이너를 중지하지 않았고, 일관성을 보장하지 않습니다."
	msgConsumerUnsafeRetry  = "확인할 수 없는 소비자가 있으면 백업을 진행하지 않습니다."

	msgPausedUnsafe       = "일시 중지된 서비스를 안전하게 중지하고 원래 상태로 되돌릴 수 없습니다."
	msgPausedUnsafeAction = "해당 서비스를 docker compose unpause로 재개하거나 stop한 뒤 다시 실행하세요."
	msgPausedUnsafeNoStop = "dcraft는 서비스를 중지하거나 백업을 만들지 않았습니다."

	msgIdentityFailed       = "스택 이름을 확인하지 못해 백업하지 않았습니다."
	msgIdentityFailedAction = "dcraft status로 스택을 확인하고, 상태 폴더(DCRAFT_STATE_DIR)의 권한과 JSON 내용을 확인하세요."

	msgStopFailed       = "서비스를 중지하지 못해 백업하지 않았습니다."
	msgStopFailedAction = "dcraft status로 현재 서비스 상태를 확인하고, 남아 있는 컨테이너를 직접 확인하세요."
	msgStopFailedNoData = "백업 파일은 만들지 않았습니다. 중지가 일부 진행됐다면 원래 실행 중이던 서비스를 다시 시작하려고 시도했습니다."

	msgArchiveFailed       = "볼륨 복사를 완료하지 못했습니다."
	msgArchiveFailedAction = "볼륨 내용의 읽기 권한과 백업 폴더의 남은 공간을 확인한 뒤 다시 실행하세요."
	msgArchiveDiskFull     = "백업 저장 공간이 부족해 볼륨 복사를 완료하지 못했습니다."
	msgArchiveDiskFullNext = "백업 폴더가 있는 파일 시스템의 남은 공간을 확보한 뒤 다시 실행하세요."
	msgArchiveReadFailed   = "볼륨 내용 일부를 읽지 못해 백업을 중단했습니다."
	msgArchiveReadRead     = "컨테이너 안에서 읽을 수 없는 파일이 있으면 일관된 백업을 만들 수 없습니다."

	msgIntegrityFailed       = "백업 파일의 무결성을 확인하지 못했습니다."
	msgIntegrityFailedAction = "백업 파일을 정상 백업으로 사용하지 마세요. 저장 공간과 파일 시스템 오류를 확인한 뒤 다시 실행하세요."

	msgCompleteFailed       = "백업 데이터는 검증됐지만 완료 표시를 확정하지 못했습니다."
	msgCompleteFailedAction = "이 백업을 정상 백업으로 사용하지 마세요. 저장 공간과 파일 시스템 권한을 확인한 뒤 다시 실행하세요."
	msgPublishFailed        = "검증된 백업을 최종 위치로 옮기지 못했습니다."
	msgPublishFailedAction  = "백업 폴더의 권한과 남은 공간을 확인하세요. .partial 폴더는 정상 백업이 아닙니다."

	msgRestoreFailed       = "백업 후 서비스 상태를 원래대로 되돌리지 못했습니다."
	msgRestoreFailedAction = "docker compose start <서비스>로 직접 다시 시작하고 dcraft status로 확인하세요."
	msgRestoreFailedData   = "백업 파일은 이미 완료됐을 수 있습니다. 아래 위치를 확인하세요."

	msgPublishedRemain  = "이미 완료된 백업은 그대로 남아 있습니다:"
	msgPublishedInvalid = "게시된 폴더: %s (완료 표시가 없어 정상 백업이 아닙니다)"
	msgPartialCleaned   = "부분 출력은 정리했습니다."
	msgPartialKept      = "부분 출력이 남아 있습니다. 정상 백업이 아니므로 사용하지 마세요."
	msgPartialLocation  = "부분 출력 위치: %s"

	msgSensitiveNotice   = "선택한 볼륨에는 비밀번호·토큰·개인정보가 포함될 수 있습니다. dcraft는 내용을 검사하거나 암호화하지 않습니다. 백업 파일을 안전하게 보관하고 공유하지 마세요."
	msgOwnershipNote     = "볼륨 백업은 컨테이너 파일의 uid/gid와 모드를 tar에 기록합니다. 호스트에서 직접 풀면 컨테이너 소유권을 완전히 복원할 수 없으므로 복원은 dcraft restore를 사용하세요."
	msgHelperImageNotice = "볼륨 복사는 Docker 컨테이너(tar)로 수행하며 이미지는 %s입니다."
	msgNoAutoDeleteNote  = "dcraft는 기존 백업을 자동으로 삭제하거나 예약·원격 업로드하지 않습니다."
	msgExcludedNotice    = "이미지, 로그, 환경 변수, 비밀 파일 전체는 백업하지 않습니다."
	msgStoppedOnlyOurs   = "이미 중지된 서비스는 시작하지 않았습니다."

	msgDoneHeader = "백업 완료"
	msgSizeLabel  = "크기"
	msgFilesLabel = "파일"

	msgPlanHeader        = "백업 계획"
	msgPlanProject       = "프로젝트: %s"
	msgPlanOutput        = "저장 폴더: %s"
	msgPlanFormat        = "형식: dcraft 백업 v1 (manifest.json + data.tar.gz, SHA-256 및 항목별 무결성)"
	msgPlanSources       = "백업 대상:"
	msgPlanServices      = "영향 서비스: %s"
	msgPlanRunning       = "실행 중(중지 예정): %s"
	msgPlanStopped       = "이미 중지됨(시작하지 않음): %s"
	msgPlanStopStep      = "서비스 중지 → 볼륨 복사 → 추출·무결성 확인 → 원자적 공개 → 중지한 서비스만 다시 시작"
	msgPlanHelper        = "복사 도구: 읽기 전용 볼륨 마운트 + tar 컨테이너 이미지 %s"
	msgPlanSensitive     = "민감정보: 선택한 볼륨 데이터에는 비밀이 포함될 수 있으며 dcraft는 검사·암호화하지 않습니다."
	msgPlanNoAutoDelete  = "기존 백업 자동 삭제·예약·원격 업로드·백업 훅은 없습니다."
	msgPlanNoneRunning   = "없음"
	msgPlanCreateDir     = "저장 폴더가 없으면 실행 시 새로 만듭니다."
	msgPlanOutputExists  = "저장 폴더가 이미 있으며 쓰기 가능 여부는 실행 직전에 다시 확인합니다."
	msgPlanLock          = "실행 중에는 같은 스택의 다른 작업을 막는 잠금을 잡고, 다른 스택은 건드리지 않습니다."
	msgPlanConsumerOther = "다른 스택의 소비자가 감지되어 백업 계획을 제공할 수 없습니다."
)

// Exported user-facing text for the registration unit.
const (
	MsgTitle           = msgTitle
	MsgSummary         = msgSummary
	MsgUsage           = msgUsage
	MsgNoBackend       = msgNoBackend
	MsgNoBackendAction = msgNoBackendAction
)

const (
	msgWizardTitle         = "데이터 백업 설정"
	msgWizardVolumeTitle   = "백업할 볼륨 선택"
	msgWizardVolumePrompt  = "현재 스택의 이름 있는 데이터 볼륨입니다. 이 명령은 연결 폴더와 익명 볼륨을 백업하지 않습니다."
	msgWizardOutputTitle   = "백업 저장 폴더"
	msgWizardOutputPrompt  = "백업 파일을 저장할 호스트 폴더를 입력하세요. dcraft는 숨은 기본 위치를 선택하지 않습니다."
	msgWizardOutputHint    = "예: ./dcraft-backups (상대 경로는 현재 작업 폴더 기준)"
	msgWizardOutputDefault = "./dcraft-backups"
	msgWizardRecheck       = "실행 직전에 볼륨·소비자·서비스 상태를 다시 확인합니다. 선택이 바뀌면 백업하지 않습니다."
)
