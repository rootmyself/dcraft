package restore

// Korean user-facing text owned by the restore feature and the shared safe
// replacement contract. Machine values (stage names, data_state, JSON keys and
// diagnostics categories) stay English.
const (
	MsgTitle   = "데이터 복원"
	MsgSummary = "검증한 백업으로 지정한 데이터 볼륨 또는 현재 스택이 선언한 연결 폴더를 안전 백업 후 교체."
	MsgUsage   = `dcraft restore --backup <백업 폴더> [--volume <볼륨 키> | --bind <연결 폴더>] [--safety-output <폴더>] [--allow-path-mismatch] [--approve data_restore] [--path <스택 폴더>]

완료된 dcraft 백업 v1만 복원. 덮어쓰기 전에 백업 형식·완료·무결성·원본/대상
대응·경로를 검사하고, 대상 서비스를 잠시 중지한 뒤 현재 데이터의 별도 안전 백업을
완성해야 교체. 복원은 병합이 아니라 교체이며, 실패·중단 시 남은 상태와 안전
백업 위치를 보고.

연결 폴더 복원 대상은 현재 스택의 Compose 파일이 선언한 폴더뿐. 백업에 기록된
호스트 경로는 컴퓨터마다 다르므로 기본은 현재 선언 경로와 같아야 하며, 다른 선언
폴더로 복원하려면 --bind로 대상을 지정하고 --allow-path-mismatch를 함께 명시해야
. 이때 경고와 매핑이 결과에 표시됨.`

	msgNoBackend       = "복원 기능이 데이터 경계와 연결되지 않았음."
	msgNoBackendAction = "dcraft를 다시 빌드하거나 설치한 뒤 실행."

	msgStageValidate     = "백업 검증"
	msgStageCoordinate   = "스택 잠금"
	msgStageResolve      = "프로젝트 확인"
	msgStageSelect       = "복원 대상 확인"
	msgStagePreview      = "복원 계획"
	msgStageConsumers    = "소비자 확인"
	msgStageSafetyCheck  = "안전 백업 공간 확인"
	msgStageStop         = "서비스 중지"
	msgStageSafetyBackup = "안전 백업"
	msgStageReplace      = "데이터 교체"
	msgStageVerify       = "복원 검증"
	msgStageRestoreState = "서비스 상태 복구"

	msgBackupRequired       = "복원할 백업 폴더를 지정하지 않았음."
	msgBackupRequiredAction = "--backup으로 완료된 dcraft 백업 v1 폴더 지정. dcraft는 기본 백업을 추측하지 않음."
	msgBackupUsageAction    = "예: dcraft restore --backup ./dcraft-backups/dcraft-proj-volume-data-20260101T000000Z --approve data_restore"

	msgArtifactInvalid       = "백업을 복원 대상으로 사용 불가."
	msgArtifactInvalidAction = "백업 폴더에 manifest.json과 data.tar.gz가 있고 완료 표시가 있는지 확인. 손상·미완료 백업은 복원하지 않음."
	msgIntegrityFailed       = "백업의 무결성 확인 실패."
	msgIntegrityFailedAction = "백업 파일이 손상됐거나 완료되지 않았음. 다른 정상 백업 선택."

	msgTargetUnsupported       = "이 백업은 이 명령으로 복원할 수 없는 데이터 원본."
	msgTargetUnsupportedAction = "백업은 이름 있는 볼륨 또는 연결 폴더 중 하나. 종류에 맞게 --volume 또는 --bind를 사용."

	msgTargetMismatch       = "백업의 원본과 복원 대상이 일치하지 않음."
	msgTargetMismatchAction = "같은 프로젝트·실행환경·볼륨/폴더의 백업을 선택하거나, 프로젝트·실행환경이 다르면 복원 불가. 연결 폴더 경로만 --bind와 --allow-path-mismatch로 명시적으로 매핑 가능. dcraft는 다른 대상을 덮어쓰지 않음."

	msgBindUnknown       = "복원 대상 연결 폴더가 현재 스택에 선언되어 있지 않음."
	msgBindUnknownAction = "docker compose config --format json의 bind source에 있는 폴더나 마운트 대상 경로를 --bind로 지정. dcraft는 임의의 호스트 폴더를 복원 대상으로 추가하지 않음."

	msgVolumeUnknown       = "복원 대상 볼륨을 현재 스택에서 찾지 못함."
	msgVolumeUnknownAction = "볼륨 키는 docker compose config의 volumes 키. --volume으로 현재 스택의 키를 지정하거나 같은 스택에서 다시 실행."

	msgTargetInvalid       = "복원 대상 데이터 사용 불가."
	msgTargetInvalidAction = "볼륨은 docker volume ls로, 연결 폴더는 존재하는 쓰기 가능한 폴더인지 확인. 파일 시스템 루트, 프로젝트 폴더, 백업/안전 백업 폴더 안은 연결 폴더 복원 대상이 될 수 없음."

	msgConsumerUnsafe       = "다른 스택이나 관리 밖 컨테이너가 대상 볼륨을 사용하고 있어 복원하지 않았음."
	msgConsumerUnsafeAction = "해당 컨테이너를 소유한 사용자가 직접 중지한 뒤 다시 실행. dcraft는 다른 스택을 중지하지 않음."
	msgPausedUnsafe         = "일시 중지된 서비스를 안전하게 중지하고 원래 상태로 되돌릴 수 없음."
	msgPausedUnsafeAction   = "해당 서비스를 재개하거나 중지한 뒤 다시 실행."

	msgIdentityFailed       = "스택 이름을 확인하지 못해 복원하지 않았음."
	msgIdentityFailedAction = "dcraft status로 스택을 확인하고 상태 폴더(DCRAFT_STATE_DIR)의 권한 확인."

	msgSafetyDirInvalid       = "안전 백업 위치 사용 불가."
	msgSafetyDirInvalidAction = "--safety-output으로 쓰기 가능한 별도 폴더 지정. 대상 데이터 안이나 백업 폴더와 겹치는 위치는 사용 불가."

	msgSpaceInsufficient       = "안전 백업에 필요한 공간이 부족해 복원을 시작하지 않았음."
	msgSpaceInsufficientAction = "안전 백업 폴더가 있는 파일 시스템의 남은 공간을 확보한 뒤 다시 실행. 원본 데이터는 변경되지 않았음."

	msgStopFailed       = "서비스를 중지하지 못해 데이터를 교체하지 않았음."
	msgStopFailedAction = "dcraft status로 서비스 상태를 확인하고 남아 있는 컨테이너를 직접 확인."

	msgSafetyFailed       = "현재 데이터의 안전 백업을 완성하지 못해 복원을 중단."
	msgSafetyFailedAction = "안전 백업 폴더의 권한과 남은 공간을 확인한 뒤 다시 실행. 원본 데이터는 교체하지 않았음."
	msgSafetyPartial      = "안전 백업 시도 위치: %s (완성되지 않았으므로 정상 백업으로 사용 금지)"

	msgReplaceFailed       = "데이터 교체 완료 실패."
	msgReplaceFailedAction = "대상 데이터가 일부만 바뀌었을 수 있음. 안전 백업 위치를 확인하고 필요하면 그 백업으로 다시 복원. dcraft는 무결성이 확인되지 않은 상태에서 서비스를 다시 시작하지 않음."
	msgVerifyFailed        = "복원한 데이터가 백업과 일치하지 않음."
	msgVerifyFailedAction  = "안전 백업으로 되돌리거나 볼륨을 직접 확인. dcraft는 이 상태를 복원 성공으로 표시하지 않으며 서비스를 다시 시작하지 않았음."

	msgRestoreStateFailed       = "데이터 복원은 완료했지만 서비스 상태를 원래대로 되돌리지 못함."
	msgRestoreStateFailedAction = "docker compose start <서비스>로 직접 시작하고 dcraft status로 확인. 데이터는 검증된 상태."

	msgDoneHeader          = "복원 완료"
	msgInterrupt           = "복원 중 대상 서비스를 잠시 중지. 짧은 서비스 중단이 발생 가능."
	msgOverwrite           = "대상 데이터의 현재 내용을 백업 내용으로 교체. 이전 파일을 남기는 병합이 아님."
	msgSafetyNotice        = "교체 전에 현재 데이터의 안전 백업을 별도 위치에 만들고 검증."
	msgOwnership           = "볼륨 복원은 root 헬퍼로 uid/gid·모드·하드링크를 보존. xattr/ACL과 FIFO·장치 등 특수 파일은 플랫폼에 따라 완전히 복원되지 않을 수 있으며 dcraft는 보존을 보장하지 않음."
	msgBindReplace         = "연결 폴더는 대상 폴더의 내용을 모두 비운 뒤 백업 내용으로 교체. 폴더 자체, 다른 호스트 폴더, 백업 보관 위치는 삭제하지 않음."
	msgBindOwnershipNotice = "연결 폴더 복원은 호스트 사용자 권한으로 일반 모드·수정 시각·심볼릭 링크·하드링크 복원. uid/gid·xattr·ACL과 setuid/setgid/sticky 같은 특수 모드 비트는 호스트 계정·파일 시스템에 따라 보존되지 않을 수 있으며, FIFO·장치 같은 특수 파일은 복원 전에 거부됨. 볼륨 복원과 같은 소유권 보존을 보장하지 않음."
	msgNoHooks             = "복원은 프로젝트 훅을 자동 실행하지 않음."
	msgNoAutoDelete        = "안전 백업을 포함한 기존 백업을 자동으로 삭제하지 않음."
	msgOnlyRunning         = "dcraft가 중지한 실행 중 서비스만 원래 상태로 되돌림. 이미 중지된 서비스는 시작하지 않음."

	msgPreviewHeader       = "복원 계획"
	msgPreviewProject      = "프로젝트: %s"
	msgPreviewArtifact     = "백업: %s (%s)"
	msgPreviewBackup       = "백업 내용: %d개 항목, %s"
	msgPreviewTarget       = "대상: %s → %s"
	msgPreviewBindTarget   = "대상 연결 폴더: %s"
	msgPreviewBindDeclared = "선언 경로: %s"
	msgPreviewCurrent      = "현재 내용: %d개 항목, %s"
	msgPreviewCurrentNone  = "현재 내용: 확인하지 못(실행 직전에 다시 확인)."
	msgPreviewSafety       = "안전 백업 위치: %s"
	msgPreviewServices     = "영향 서비스: %s"
	msgPreviewRunning      = "실행 중(중지 예정): %s"
	msgPreviewStopped      = "이미 중지됨(시작하지 않음): %s"
	msgPreviewExplicit     = "명시적 매핑: 백업과 대상이 다르며 경고 표시."
	msgPreviewExplicitPath = "명시적 경로 매핑: 백업의 기록 경로와 현재 선언 폴더가 다름. 경고 표시."
	msgPreviewWarnings     = "경고:"
	msgPreviewSteps        = "서비스 중지 → 현재 데이터 안전 백업·검증 → 대상 비우기 → 백업 압축 해제 → 백업과 항목별 대조 → 중지한 서비스만 복구"
	msgPreviewLock         = "실행 중에는 같은 스택의 다른 작업을 막고 다른 스택은 건드리지 않음."
	msgPreviewNone         = "없음"

	msgWizardTitle       = "데이터 복원 설정"
	msgWizardArtifact    = "복원할 백업 선택"
	msgWizardArtifactWhy = "완료된 dcraft 백업 v1만 표시. 볼륨과 연결 폴더 백업을 모두 선택 가능. 다른 경로의 백업은 CLI의 --backup으로 지정."
	msgWizardArtifactIn  = "복원할 백업 폴더 경로 입력."
	msgWizardArtifactDef = "./dcraft-backups"
	msgWizardTarget      = "복원 대상 선택"
	msgWizardTargetWhy   = "백업에 기록된 대상(볼륨 또는 현재 스택이 선언한 연결 폴더)을 기본으로 사용. 다른 대상에 명시적으로 매핑하려면 대상 항목 선택. 프로젝트·실행환경은 항상 같아야 하며, 연결 폴더 경로가 다르면 CLI에서 --bind와 --allow-path-mismatch 사용 필요."
	msgWizardTargetAuto  = "백업에 기록된 대상 사용 (권장)"
	msgWizardSafety      = "안전 백업 폴더"
	msgWizardSafetyWhy   = "현재 볼륨 데이터를 복원 전에 백업할 별도 호스트 폴더. 대상 볼륨 안은 사용 불가."
	msgWizardSafetyDef   = "./dcraft-safety"
	msgWizardRecheck     = "실행 직전에 백업 무결성·대상 볼륨·소비자·서비스 상태를 다시 확인. 선택이 바뀌면 복원하지 않음."

	// Exported text for the registration unit.
	MsgNoBackend       = msgNoBackend
	MsgNoBackendAction = msgNoBackendAction

	msgDataStateUntouched  = "untouched"
	msgDataStateUncertain  = "uncertain_partial"
	msgDataStateReplaced   = "replaced_verified"
	msgDataStateLabel      = "데이터 상태: %s"
	msgStageLabel          = "단계: %s"
	msgSafetyLocationLabel = "안전 백업 위치: %s"
	msgArtifactLocation    = "백업 위치: %s"
	msgRecoveryUntouched   = "원본 데이터는 변경되지 않았음. 서비스 상태만 원래대로 복구."
	msgRecoverySafety      = "복구: dcraft restore --backup %s --approve data_restore"
	msgRecoveryUncertain   = "대상 데이터가 일부만 바뀌었을 수 있음. 안전 백업으로 되돌리거나 dcraft status로 상태 확인."
	msgRecoveryNoSafety    = "완성된 안전 백업이 없음. 원본 데이터를 직접 확인."
	msgCanceledWithSafety  = "복원이 중단되었음. %s, %s, 안전 백업 위치: %s. 무결성이 확인되지 않아 서비스를 다시 시작하지 않았음."
	msgCanceledUntouched   = "복원이 중단되었음. %s, %s. 원본 데이터는 교체되지 않았으며 중지한 서비스만 다시 시작."
	msgCanceledNoSafety    = "복원이 중단되었음. %s, %s. 완성된 안전 백업이 없음."
	msgCanceledReplaced    = "복원이 중단되었지만 데이터 교체와 검증은 완료되었음. %s, %s, 안전 백업 위치: %s. 무결성이 확인된 범위에서 서비스를 복구."
	msgCanceledActionOne   = "복원을 다시 실행하기 전에 대상 데이터와 서비스 상태 확인."
	msgCanceledActionTwo   = "dcraft status로 현재 상태 확인."
)
