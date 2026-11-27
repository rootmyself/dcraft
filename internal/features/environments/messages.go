package environments

// Korean user-facing text for the environments command. Command names and
// machine values stay English.
const (
	msgTitle   = "실행환경 별칭"
	msgSummary = "Compose 파일·환경 파일·프로필 조합을 실행환경 별칭으로 만들고 확인·제거."
	// The TUI menu offers two entries over this one feature, under one
	// heading. They are menu labels only: each screen keeps its own title.
	msgMenuGroup         = "실행환경"
	msgMenuGroupSummary  = "Compose 입력 조합을 별칭으로 관리."
	msgMenuCreate        = "생성"
	msgMenuCreateSummary = "실행환경 별칭을 만들거나 제거."
	msgMenuSwitch        = "전환"
	msgMenuSwitchSummary = "작업할 실행환경을 골라 대상 전환."
	msgUsage             = "dcraft environments --add --name <별칭> [--file <Compose 파일> ...] [--env-file <파일> ...] [--profile <프로필> ...]\n" +
		"  dcraft environments --list\n" +
		"  dcraft environments --remove --name <별칭>\n\n" +
		"별칭은 Compose 입력만 묶음. 병합 규칙이나 별도 배포 명세가 아님.\n" +
		"별칭이 하나라도 있으면 모든 작업에서 --env 또는 TUI 선택 필요. 첫 항목은 자동 선택하지 않음.\n" +
		"외부 볼륨·bind mount·네트워크·고정 호스트 포트는 별칭으로 격리되지 않음.\n" +
		"환경 파일의 값은 읽거나 저장하지 않고 파일 경로만 설정에 기록."

	msgFlagAdd    = "실행환경 별칭 추가. --name이 필요."
	msgFlagList   = "등록된 실행환경 별칭 표시."
	msgFlagRemove = "등록된 실행환경 별칭 제거. --name이 필요."
	msgFlagName   = "실행환경 별칭 (소문자·숫자·하이픈·밑줄, 32자 이내)"

	msgActionRequired = "동작 명시 필요: --add, --list, --remove"
	msgActionConflict = "한 번에 하나의 동작만 지정: --add, --list, --remove"
	msgMissingName    = "필요한 입력이 없음: --name <별칭>"
	msgStageSave      = "별칭 저장"
	msgStageRemove    = "별칭 제거"
	msgStageList      = "별칭 확인"

	msgInputActionOne = "dcraft environments --help로 입력 형식 확인."
	msgInputActionTwo = "별칭 이름과 Compose 파일·환경 파일 경로 확인."

	msgWizardTitle          = "실행환경 생성·제거"
	msgWizardNameTitle      = "별칭 이름"
	msgWizardNamePrompt     = "이 별칭을 부를 이름을 직접 입력. 자동으로 정해지지 않음."
	msgWizardNameHint       = "예: local · staging · production"
	msgWizardFilesTitle     = "Compose 파일"
	msgWizardFilesPrompt    = "이 별칭이 사용할 Compose 파일을 순서대로 입력."
	msgWizardFilesHint      = "한 줄에 하나 · 예: compose.yaml · compose.prod.yaml · deploy/compose.yaml"
	msgWizardEnvFilesTitle  = "환경 파일"
	msgWizardEnvFilesPrompt = "명시적으로 전달할 환경 파일 입력. 없으면 비움."
	msgWizardEnvFilesHint   = "한 줄에 하나 · 예: .env.local · .env.production · config/api.env (값은 읽지 않음)"
	msgWizardProfilesTitle  = "Compose 프로필"
	msgWizardProfilesPrompt = "활성화할 Compose 프로필 입력. 없으면 비움."
	msgWizardProfilesHint   = "한 줄에 하나 · 예: web · worker · monitoring"

	msgWizardPreviewTitle   = "실행환경 별칭 저장 확인"
	msgWizardConfigPrefix   = "저장 위치: "
	msgWizardPreviewConfirm = "enter 저장 · left 이전 단계 · esc 취소"
	msgDisclosure           = "주의: 외부 볼륨·bind mount·네트워크·고정 호스트 포트는 실행환경끼리 공유될 수 있음."

	msgPresentTitle     = "실행환경 별칭"
	msgPresentConfig    = "설정 파일"
	msgPresentAdded     = "별칭 저장."
	msgPresentRemoved   = "별칭 제거. 실행 중인 스택과 데이터는 그대로 둠."
	msgPresentListTitle = "등록된 별칭:"
	msgPresentEmpty     = "등록된 실행환경 별칭이 없음. 이 폴더는 표준 Compose 입력 사용."
	msgPresentFiles     = "Compose 파일"
	msgPresentEnvFiles  = "환경 파일"
	msgPresentProfiles  = "프로필"
	msgPresentNone      = "(없음)"
	msgPresentAddHint   = "추가: dcraft environments --add --name <별칭> [--file ...] [--env-file ...] [--profile ...]"
	msgPresentTUIHint   = "대화형 화면에서는 같은 내용을 단계별로 입력하고 저장 전에 미리 볼 수 있음."
	msgPresentLimits    = "실행환경은 Compose 입력과 dcraft 상태·이력을 구분하지만 외부 볼륨·bind mount·네트워크·고정 호스트 포트는 격리하지 않음."
)
