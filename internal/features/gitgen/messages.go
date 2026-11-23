package gitgen

// Korean user-facing registration text owned by the Git generator. Command
// names, flag names and machine values stay English.
const (
	msgTitle   = "Git 호스트 스크립트 생성"
	msgSummary = "저장소·ref·받을 폴더를 물어 보고 검토할 수 있는 Git 갱신 스크립트를 만듦. 생성은 등록·실행·신뢰 승인이 아니며 원격에 접속하지 않음."
	msgUsage   = "dcraft generate-git-hook --repo <저장소> --ref <branch|tag|전체 SHA> --dest <받을 폴더> --output <스크립트 경로> [--stage pre|post] --approve file_overwrite\n\n" +
		"생성한 스크립트는 자동 등록·자동 실행되지 않음. 출력에 표시되는 dcraft hooks --add 명령으로 직접 등록하고, 첫 실행에는 별도의 script_trust 승인이 필요."

	msgFlagRepo  = "Git 저장소 URL 또는 로컬 경로. 인증 정보가 들어간 URL은 거부."
	msgFlagRef   = "branch, tag 또는 40/64자 전체 SHA. refs/heads/..., refs/tags/... 형식도 허용."
	msgFlagDest  = "받을 폴더. 프로젝트 폴더 안이어야 하며 프로젝트 루트·스크립트 폴더와 같을 수 없음."
	msgFlagOut   = "생성할 스크립트 파일 경로. 부모 폴더가 없으면 만들고 0755로 저장."
	msgFlagStage = "실행 시점: pre(up/update 전, 실패 시 본 작업 차단) 또는 post(본 작업 후, 실패는 경고)."

	msgStageValidate = "입력 검증"
	msgStageWrite    = "스크립트 저장"

	msgInputActionOne = "저장소·ref·받을 폴더·스크립트 경로 확인."
	msgInputActionTwo = "받을 폴더와 스크립트 경로는 프로젝트 폴더 안이어야 함."
	msgWriteActionOne = "스크립트 상위 폴더의 쓰기 권한과 남은 공간 확인."
	msgWriteActionTwo = "기존 경로가 심볼릭 링크나 폴더가 아닌 일반 파일인지 확인."

	msgPlanHeader      = "Git 호스트 스크립트 생성 계획"
	msgPlanNoNetwork   = "지금은 원격 저장소에 접속하지 않음. Git 명령도 실행하지 않고 스크립트 파일만 저장."
	msgPlanNoRegister  = "이 작업은 .dcraft.yaml을 수정하거나 스크립트를 자동 등록하지 않음. 실행하거나 자동 신뢰하지도 않음."
	msgPlanInputs      = "입력"
	msgPlanExecution   = "실행 위치와 저장"
	msgPlanEffects     = "스크립트가 실행될 때 하는 일"
	msgPlanPrereq      = "전제 조건"
	msgPlanFailure     = "실패 시"
	msgPlanEdit        = "수정 방법"
	msgPlanApproval    = "저장 승인"
	msgPlanNext        = "다음 단계 (자동 등록·자동 실행 아님)"
	msgPlanNewFile     = "새 파일을 만듦."
	msgPlanReplaceFile = "기존 파일을 교체. 기존 내용은 사라짐."
	msgPlanKindAuto    = "자동 판별(auto): 실행할 때 원격에서 branch/tag를 확인하고, 같은 이름이 둘 다 있으면 추측하지 않고 실패."
	msgPlanKindBranch  = "branch: refs/heads/ 네임스페이스로 fetch."
	msgPlanKindTag     = "tag: refs/tags/ 네임스페이스로 fetch하고 annotated tag는 commit으로 peel."
	msgPlanKindCommit  = "전체 SHA: 짧은 SHA 없이 해당 commit을 직접 fetch. 서버가 임의 커밋 fetch 허용 필요."
	msgPlanEffectsBody = "  - 받을 폴더가 Git 저장소가 아니면 clone.\n" +
		"  - 이미 같은 origin의 저장소면 fetch 후 요청한 ref를 checkout.\n" +
		"  - 작업 트리에 커밋되지 않은 변경(추적/미추적)이 있으면 아무것도 바꾸지 않고 실패.\n" +
		"  - 마지막으로 받은 commit과 현재 HEAD가 다르면 로컬 commit/checkout으로 보고 중단.\n" +
		"  - git reset --hard, 강제 checkout, 폴더 삭제, 임의 경로 덮어쓰기, 도구 설치는 하지 않음.\n" +
		"  - checkout 전에 이전 HEAD를 refs/dcraft/previous에 보관하고, checkout은 detached HEAD일 수 있음.\n" +
		"  - 받은 폴더를 Compose build context/볼륨 등 실제 입력으로 연결. dcraft는 기존 Compose를 자동 수정하지 않음."
	msgPlanPrereqBody = "  - 실행 시 git CLI가 PATH에 있어야 함. 생성 시에는 Git이 없어도 됨.\n" +
		"  - 인증은 기존 credential helper 또는 SSH agent를 사용. 스크립트 파일에는 인증 정보가 없음.\n" +
		"  - 저장소 URL에 사용자 이름/비밀번호/토큰 넣기 금지. 스크립트는 그런 URL을 거부.\n" +
		"  - AI 호출이나 별도 서비스 계정이 필요하지 않음. 저장소 접근은 사용자의 기존 Git 인증을 따름.\n" +
		"  - 전체 SHA fetch는 서버 정책에 따라 거부될 수 있음. 그때는 branch 또는 tag를 사용."
	msgPlanFailureBody = "  - 사전(pre) 훅 실패는 dcraft가 본 작업(up/update)을 시작하지 않게 함.\n" +
		"  - 사후(post) 훅 실패는 이미 완료된 본 작업과 별도로 경고로 보고됨.\n" +
		"  - 이미 받은 파일은 남아 있을 수 있고 dcraft가 자동으로 되돌리지 않음."
	msgPlanEditBody = "  - 스크립트의 REPO, REF, REF_KIND, DEST 값을 직접 고치거나 이 명령으로 다시 생성.\n" +
		"  - 파일 내용이 바뀌면 script_trust 승인을 다시 받아야 함."

	msgDoneHeader     = "Git 호스트 스크립트 저장."
	msgDoneScript     = "스크립트"
	msgDoneDest       = "받을 폴더"
	msgDoneRef        = "ref"
	msgDoneSave       = "저장"
	msgDoneSHA        = "내용 식별자"
	msgDoneNotRun     = "아직 등록하거나 실행하지 않았음. 다음 명령으로 직접 등록."
	msgDoneTrust      = "첫 실행 또는 내용 변경 후에는 별도의 script_trust 승인이 필요."
	msgDoneGeneration = "생성 시 원격 저장소에 접속하지 않았음."

	msgWizardTitle       = "Git 호스트 스크립트 생성"
	msgWizardRepoTitle   = "저장소"
	msgWizardRepoPrompt  = "Git 저장소 URL 또는 로컬 경로 입력."
	msgWizardRepoHint    = "예: https://github.com/acme/web.git · git@github.com:acme/api.git (인증 정보가 든 URL은 거부)"
	msgWizardRefTitle    = "ref"
	msgWizardRefPrompt   = "branch, tag 또는 전체 SHA를 입력."
	msgWizardRefHint     = "예: main · v1.2.0 · refs/tags/v1.2.0 (40자 전체 SHA 가능, 짧은 SHA는 지원하지 않음)"
	msgWizardDestTitle   = "받을 폴더"
	msgWizardDestPrompt  = "프로젝트 폴더 안에서 소스를 받을 폴더 입력."
	msgWizardDestHint    = "예: src · services/api · vendor/web (프로젝트 루트나 스크립트 폴더와 같을 수 없음)"
	msgWizardOutTitle    = "스크립트 경로"
	msgWizardOutPrompt   = "생성할 스크립트 파일 경로 입력."
	msgWizardOutHint     = "예: scripts/git-sync.sh · scripts/fetch-api.sh · deploy/update-web.sh"
	msgWizardStageTitle  = "실행 시점"
	msgWizardStagePrompt = "up/update의 어느 시점에 실행할지 선택."
	msgWizardPreLabel    = "사전 (본 작업 전, 실패 시 차단)"
	msgWizardPostLabel   = "사후 (본 작업 완료 후, 실패는 경고)"
)
