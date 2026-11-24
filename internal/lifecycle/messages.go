package lifecycle

// Korean user-facing text owned by the lifecycle features.
const (
	msgStageResolveProject = "프로젝트 확인"
	msgStageAllocateName   = "프로젝트 이름 확인"
	msgStageCoordinate     = "같은 스택 작업 확인"
	msgStageObserve        = "현재 상태 확인"

	msgUnknownAction         = "지원하지 않는 스택 수명 작업."
	msgUnknownActionNext     = "dcraft --help로 사용 가능한 명령 확인."
	msgNoLifecycle           = "스택 수명 작업 연결이 준비되지 않았음."
	msgNoLifecycleNext       = "dcraft를 다시 설치하거나 빌드한 뒤 실행."
	msgDownIsProjectWide     = "down은 스택 전체에만 적용됨."
	msgDownIsProjectWideNext = "서비스를 지정하지 말고 다시 실행."

	msgEmptyService          = "빈 서비스 이름은 선택 불가."
	msgEmptyServiceNext      = "-s/--service에 Compose 서비스 이름 지정."
	msgUnknownService        = "선택한 서비스가 Compose 설정에 없음: %s"
	msgAvailableServices     = "사용 가능한 서비스: "
	msgUnknownServiceNextOne = "dcraft status로 서비스 이름 확인."
	msgUnknownServiceNextTwo = "-s/--service에는 현재 Compose 설정에 있는 서비스 이름 지정."

	msgNameConflict          = "프로젝트 이름 '%s'을(를) 다른 스택이 사용 중."
	msgNameConflictNextOne   = "다른 이름을 -p/--project-name으로 지정."
	msgNameConflictNextTwo   = "그 폴더가 더 이상 필요 없으면 해당 폴더에서 dcraft down 후 로컬 상태의 이름 기록을 정리."
	msgNameConflictNextThree = "dcraft는 다른 스택의 이름을 바꾸거나 해당 스택을 조작하지 않음."

	msgStackBusy        = "같은 스택에서 다른 작업이 진행 중."
	msgStackBusyNextOne = "진행 중인 작업이 끝난 뒤 다시 실행."
	msgStackBusyNextTwo = "dcraft는 같은 스택의 변경 작업을 동시에 실행하지 않음."

	msgStateFailed        = "로컬 상태를 읽거나 저장 실패."
	msgStateFailedNextOne = "DCRAFT_STATE_DIR 폴더의 쓰기 권한과 여유 공간 확인."
	msgStateFailedNextTwo = "dcraft status로 현재 스택 상태 확인."

	msgLifecycleFailed = "스택 %s에 실패. %s"
	msgStateLed        = "현재 상태: "
	msgStateUnreadable = "현재 상태: 확인 실패."
	msgStateEmpty      = "서비스 없음"

	msgObserveFailed = "현재 상태 확인 실패. dcraft status로 다시 확인."
	msgRenameNotice  = "이 대상은 이전에 '%s' 이름 사용. 이제 '%s' 이름 사용. 이전 프로젝트의 컨테이너는 dcraft 관리 밖에 남을 수 있음."

	msgRecoverUpOne      = "dcraft status로 현재 상태 확인."
	msgRecoverUpTwo      = "docker compose logs로 서비스 로그 확인."
	msgRecoverUpThree    = "원인을 해결한 뒤 dcraft up을 다시 실행."
	msgRecoverStopOne    = "dcraft status로 남은 컨테이너 상태 확인."
	msgRecoverStopTwo    = "필요하면 dcraft stop을 다시 실행."
	msgRecoverDownOne    = "dcraft status로 남은 컨테이너 확인."
	msgRecoverDownTwo    = "필요하면 dcraft down을 다시 실행."
	msgRecoverDownThree  = "dcraft의 down은 데이터 볼륨을 삭제하지 않음."
	msgRecoverRestartOne = "dcraft status로 컨테이너 상태 확인."
	msgRecoverRestartTwo = "원인을 확인하려면 docker compose logs를 사용."
	msgRecoverGeneric    = "dcraft status로 현재 상태 확인."

	// Successful result text.
	msgDoneUp      = "스택 시작."
	msgDoneStop    = "스택 중지."
	msgDoneDown    = "스택 제거."
	msgDoneRestart = "스택 재시작."

	msgEffectUp      = "현재 Compose 설정을 적용해 컨테이너 시작. 짧은 중단이 있을 수 있으며 전체 원자성을 보장하지 않음."
	msgEffectStop    = "컨테이너 중지. 컨테이너와 데이터는 유지되며 dcraft up으로 다시 시작 가능."
	msgEffectDown    = "컨테이너와 기본 네트워크 제거. 데이터 볼륨은 삭제하지 않았음."
	msgEffectRestart = "컨테이너를 다시 시작. 새 설정이나 새 이미지는 적용하지 않았음."

	msgProjectLabel   = "프로젝트"
	msgFolderLabel    = "폴더"
	msgScopeLabel     = "범위"
	msgScopeStack     = "스택 전체"
	msgScopeServices  = "선택한 서비스"
	msgEffectLabel    = "효과"
	msgStateLabel     = "현재 상태"
	msgNoticesLabel   = "알림"
	msgNextLabel      = "다음 행동"
	msgStateMissing   = "확인 실패."
	msgNoPayload      = "작업 결과 표시 불가."
	msgNameAuto       = "자동 배정"
	msgNameExplicit   = "직접 지정 (-p)"
	msgNameConfigured = "Compose 설정 이름"
	msgNameExisting   = "기존 프로젝트 재사용"
	msgNameUnknown    = "이름 확인 안 됨"

	msgNextStatus         = "상태 확인: dcraft status"
	msgNextStop           = "중지: dcraft stop (컨테이너·데이터 유지)"
	msgNextDown           = "제거: dcraft down (데이터 볼륨 유지)"
	msgNextUp             = "다시 시작: dcraft up"
	msgNextRestartToApply = "설정을 적용하려면 dcraft up을 실행. restart는 설정·이미지를 갱신하지 않음."
	msgNextVolumesKept    = "데이터 볼륨은 자동으로 삭제되지 않았음."
	msgNextObserveFailed  = "현재 상태를 보려면 dcraft status를 실행."

	// The same four actions in the interactive screen's own terms. A reader who
	// works only in the TUI has no reason to type the command, so the document
	// names the menu entry instead. "메뉴" is the footer key that returns here.
	msgDocNextStatus         = "상태 확인: 메뉴에서 스택 상태 선택."
	msgDocNextStop           = "중지: 메뉴에서 스택 중지 선택 (컨테이너·데이터 유지)."
	msgDocNextDown           = "제거: 메뉴에서 스택 제거 선택 (데이터 볼륨 유지)."
	msgDocNextUp             = "다시 시작: 메뉴에서 스택 시작 선택."
	msgDocNextRestartToApply = "설정을 적용하려면 메뉴에서 갱신을 실행. 재시작은 설정·이미지를 갱신하지 않음."
	msgDocNextObserveFailed  = "현재 상태를 보려면 메뉴에서 스택 상태 선택."
)

// TUI document wording. The CLI Present output keeps its own sentences; these
// are only used by the structured document the TUI renders. State and health
// labels come from internal/stack/present so the status screen and this one
// cannot drift apart.
const (
	msgTitle           = "스택 작업"
	msgColumnService   = "서비스"
	msgColumnState     = "상태"
	msgColumnInstances = "인스턴스"
	msgColumnHealth    = "health"
	msgColumnImage     = "이미지"
	msgColumnPort      = "포트"
	msgNoPort          = "-"
	msgAllInterfaces   = "모든 인터페이스"
	msgDynamicPort     = "동적 호스트 포트"
	msgConfiguredPort  = " (설정값)"

	// The notices badge counts reports the operation attached to its result.
	msgBadgeNotices = "알림 %d"
)
