package lifecycle

// Korean user-facing text owned by the lifecycle features.
const (
	msgStageResolveProject = "프로젝트 확인"
	msgStageAllocateName   = "프로젝트 이름 확인"
	msgStageCoordinate     = "같은 스택 작업 확인"
	msgStageObserve        = "현재 상태 확인"

	msgUnknownAction         = "지원하지 않는 스택 수명 작업입니다."
	msgUnknownActionNext     = "dcraft --help로 사용 가능한 명령을 확인하세요."
	msgNoLifecycle           = "스택 수명 작업 연결이 준비되지 않았습니다."
	msgNoLifecycleNext       = "dcraft를 다시 설치하거나 빌드한 뒤 실행하세요."
	msgDownIsProjectWide     = "down은 스택 전체에만 적용됩니다."
	msgDownIsProjectWideNext = "서비스를 지정하지 말고 다시 실행하세요."

	msgEmptyService          = "빈 서비스 이름은 선택할 수 없습니다."
	msgEmptyServiceNext      = "-s/--service에 Compose 서비스 이름을 지정하세요."
	msgUnknownService        = "선택한 서비스가 Compose 설정에 없습니다: %s"
	msgAvailableServices     = "사용 가능한 서비스: "
	msgUnknownServiceNextOne = "dcraft status로 서비스 이름을 확인하세요."
	msgUnknownServiceNextTwo = "-s/--service에는 현재 Compose 설정에 있는 서비스 이름을 지정하세요."

	msgNameConflict          = "프로젝트 이름 '%s'을(를) 다른 스택이 사용 중입니다."
	msgNameConflictNextOne   = "다른 이름을 -p/--project-name으로 지정하세요."
	msgNameConflictNextTwo   = "그 폴더가 더 이상 필요 없으면 해당 폴더에서 dcraft down 후 로컬 상태의 이름 기록을 정리하세요."
	msgNameConflictNextThree = "dcraft는 다른 스택의 이름을 바꾸거나 해당 스택을 조작하지 않습니다."

	msgStackBusy        = "같은 스택에서 다른 작업이 진행 중입니다."
	msgStackBusyNextOne = "진행 중인 작업이 끝난 뒤 다시 실행하세요."
	msgStackBusyNextTwo = "dcraft는 같은 스택의 변경 작업을 동시에 실행하지 않습니다."

	msgStateFailed        = "로컬 상태를 읽거나 저장하지 못했습니다."
	msgStateFailedNextOne = "DCRAFT_STATE_DIR 폴더의 쓰기 권한과 여유 공간을 확인하세요."
	msgStateFailedNextTwo = "dcraft status로 현재 스택 상태를 확인하세요."

	msgLifecycleFailed = "스택 %s에 실패했습니다. %s"
	msgStateLed        = "현재 상태: "
	msgStateUnreadable = "현재 상태: 확인하지 못했습니다."
	msgStateEmpty      = "서비스 없음"

	msgObserveFailed = "현재 상태를 확인하지 못했습니다. dcraft status로 다시 확인하세요."
	msgRenameNotice  = "이 대상은 이전에 '%s' 이름을 사용했습니다. 이제 '%s' 이름을 사용하며, 이전 프로젝트의 컨테이너는 dcraft 관리 밖에 남을 수 있습니다."

	msgRecoverUpOne      = "dcraft status로 현재 상태를 확인하세요."
	msgRecoverUpTwo      = "docker compose logs로 서비스 로그를 확인하세요."
	msgRecoverUpThree    = "원인을 해결한 뒤 dcraft up을 다시 실행하세요."
	msgRecoverStopOne    = "dcraft status로 남은 컨테이너 상태를 확인하세요."
	msgRecoverStopTwo    = "필요하면 dcraft stop을 다시 실행하세요."
	msgRecoverDownOne    = "dcraft status로 남은 컨테이너를 확인하세요."
	msgRecoverDownTwo    = "필요하면 dcraft down을 다시 실행하세요."
	msgRecoverDownThree  = "dcraft의 down은 데이터 볼륨을 삭제하지 않습니다."
	msgRecoverRestartOne = "dcraft status로 컨테이너 상태를 확인하세요."
	msgRecoverRestartTwo = "원인을 확인하려면 docker compose logs를 사용하세요."
	msgRecoverGeneric    = "dcraft status로 현재 상태를 확인하세요."

	// Successful result text.
	msgDoneUp      = "스택을 시작했습니다."
	msgDoneStop    = "스택을 중지했습니다."
	msgDoneDown    = "스택을 제거했습니다."
	msgDoneRestart = "스택을 재시작했습니다."

	msgEffectUp      = "현재 Compose 설정을 적용해 컨테이너를 시작했습니다. 짧은 중단이 있을 수 있으며 전체 원자성을 보장하지 않습니다."
	msgEffectStop    = "컨테이너를 중지했습니다. 컨테이너와 데이터는 유지되며 dcraft up으로 다시 시작할 수 있습니다."
	msgEffectDown    = "컨테이너와 기본 네트워크를 제거했습니다. 데이터 볼륨은 삭제하지 않았습니다."
	msgEffectRestart = "컨테이너를 다시 시작했습니다. 새 설정이나 새 이미지는 적용하지 않았습니다."

	msgProjectLabel   = "프로젝트"
	msgFolderLabel    = "폴더"
	msgScopeLabel     = "범위"
	msgScopeStack     = "스택 전체"
	msgScopeServices  = "선택한 서비스"
	msgEffectLabel    = "효과"
	msgStateLabel     = "현재 상태"
	msgNoticesLabel   = "알림"
	msgNextLabel      = "다음 행동"
	msgStateMissing   = "확인하지 못했습니다."
	msgNoPayload      = "작업 결과를 표시할 수 없습니다."
	msgNameAuto       = "자동 배정"
	msgNameExplicit   = "직접 지정 (-p)"
	msgNameConfigured = "Compose 설정 이름"
	msgNameExisting   = "기존 프로젝트 재사용"
	msgNameUnknown    = "이름 확인 안 됨"

	msgNextStatus         = "상태 확인: dcraft status"
	msgNextStop           = "중지: dcraft stop (컨테이너·데이터 유지)"
	msgNextDown           = "제거: dcraft down (데이터 볼륨 유지)"
	msgNextUp             = "다시 시작: dcraft up"
	msgNextRestartToApply = "설정을 적용하려면 dcraft up을 실행하세요. restart는 설정·이미지를 갱신하지 않습니다."
	msgNextVolumesKept    = "데이터 볼륨은 자동으로 삭제되지 않았습니다."
	msgNextObserveFailed  = "현재 상태를 보려면 dcraft status를 실행하세요."
)
