package s3gen

// Korean user-facing registration text owned by the S3 generator. Command
// names, flag names and machine values stay English.
const (
	msgTitle   = "S3 호스트 스크립트 생성"
	msgSummary = "S3 원본·받을 폴더·방식을 물어 보고 검토할 수 있는 배포 스크립트를 만듦. 생성은 등록·실행·신뢰 승인이 아니며 S3에 접속하지 않음."
	msgUsage   = "dcraft generate-s3-hook --source <s3://bucket/prefix|s3://bucket/path/artifact.tar.gz> --mode <sync|tar> --dest <받을 폴더> --output <스크립트 경로> [--aws-profile <이름>] --approve file_overwrite\n\n" +
		"생성한 스크립트는 자동 등록·자동 실행되지 않음. 출력에 표시되는 dcraft hooks --add 명령으로 직접 등록하고, 첫 실행에는 별도의 script_trust 승인이 필요."

	msgFlagSource     = "S3 원본. sync는 디렉터리 접두사(s3://bucket/prefix), tar는 단일 .tar.gz 객체(s3://bucket/path/artifact.tar.gz)를 지정."
	msgFlagMode       = "가져오기 방식: sync(디렉터리 동기화, 삭제 동기화 없음) 또는 tar(.tar.gz 다운로드·검증·해제 후 복사)."
	msgFlagDest       = "받을 폴더. 프로젝트 폴더 안이어야 하며 프로젝트 루트·스크립트 폴더와 같을 수 없음."
	msgFlagOut        = "생성할 스크립트 파일 경로. 부모 폴더가 없으면 만들고 0755로 저장."
	msgFlagAwsProfile = "선택: 기존 AWS CLI 프로필 이름(비밀값 아님). 지정하지 않으면 AWS CLI의 기본 자격 증명 확인 순서 사용."

	msgStageValidate = "입력 검증"
	msgStageWrite    = "스크립트 저장"

	msgInputActionOne = "S3 원본·방식·받을 폴더·스크립트 경로 확인."
	msgInputActionTwo = "sync는 s3://bucket/prefix, tar는 단일 s3://bucket/path/artifact.tar.gz가 필요. 경로는 프로젝트 폴더 안 사용."
	msgWriteActionOne = "스크립트 상위 폴더의 쓰기 권한과 남은 공간 확인."
	msgWriteActionTwo = "기존 경로가 심볼릭 링크나 폴더가 아닌 일반 파일인지 확인."

	msgPlanHeader      = "S3 호스트 스크립트 생성 계획"
	msgPlanNoNetwork   = "지금은 S3에 접속하지 않음. aws CLI도 실행하지 않고 스크립트 파일만 저장."
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

	msgPlanPrereqBody = "  - 실행 시 aws CLI가 PATH에 있어야 함. 이 스크립트는 AWS CLI를 자동 설치하지 않음.\n" +
		"  - 기존 AWS CLI 구성(환경 변수, ~/.aws, SSO, 역할 등)을 사용. aws configure를 실행하거나 자격 증명을 저장·출력하지 않음.\n" +
		"  - tar 방식은 tar와 gzip이 필요. 생성 시에는 필요하지 않음.\n" +
		"  - S3 원본에 사용자 이름/비밀번호/토큰/미리 서명된 쿼리를 넣을 수 없음. 생성기는 그런 주소를 거부.\n" +
		"  - AWS CLI가 출력하는 오류는 사용자의 AWS CLI 출력이며 dcraft가 제어하지 않음.\n" +
		"  - 호스트 스크립트는 사용자 권한으로 실행됨. dcraft는 샌드박싱하거나 자동으로 되돌리지 않음."

	msgPlanFailureBody = "  - 사전(pre) 훅 실패는 dcraft가 본 작업(up/update)을 시작하지 않게 함.\n" +
		"  - sync 도중 실패하면 받을 폴더에 일부 파일이 남아 있을 수 있음.\n" +
		"  - tar 방식은 다운로드·무결성·경로·링크 검증 실패 시 받을 폴더를 변경하지 않고 임시 파일을 정리.\n" +
		"  - tar 복사 도중 실패하면 받을 폴더에 일부 파일이 남아 있을 수 있음.\n" +
		"  - dcraft는 실패한 다운로드나 복사를 자동으로 되돌리지 않음."
	msgPlanEditBody = "  - 스크립트의 SRC, DEST, PROFILE 값을 직접 고치거나 이 명령으로 다시 생성.\n" +
		"  - 파일 내용이 바뀌면 script_trust 승인을 다시 받아야 함."

	msgDoneHeader = "S3 호스트 스크립트 저장."
	msgDoneScript = "스크립트"
	msgDoneDest   = "받을 폴더"
	msgDoneSource = "S3 원본"
	msgDoneMode   = "방식"
	msgDoneSave   = "저장"
	msgDoneSHA    = "내용 식별자"
	msgDoneNotRun = "아직 등록하거나 실행하지 않았음. 다음 명령으로 직접 등록."
	// The interactive screen points at the menu entry that registers and
	// approves the script, so the reader never has to type the commands.
	msgDocNext        = "다음 행동: 메뉴에서 호스트 스크립트를 열어 등록하고 신뢰 승인."
	msgDoneTrust      = "첫 실행 또는 내용 변경 후에는 별도의 script_trust 승인이 필요."
	msgDoneGeneration = "생성 시 S3에 접속하지 않았음."
	msgDoneNoDelete   = "sync는 삭제 동기화 옵션을 넣지 않았음. 받을 폴더의 기존 파일은 그대로 남음."
	msgDoneTarSafe    = "tar는 무결성·경로·링크 검증에 실패하면 받을 폴더를 바꾸지 않음. 기존 파일은 삭제하지 않음."
	msgDoneAWS        = "AWS CLI 오류는 사용자의 AWS CLI 출력. dcraft는 자격 증명을 저장하거나 출력하지 않음."
	msgDoneOutside    = "프로젝트 밖 경로가 포함되었음. 이동·백업 시 경로를 직접 확인."

	msgWizardTitle        = "S3 호스트 스크립트 생성"
	msgWizardSourceTitle  = "S3 원본"
	msgWizardSourcePrompt = "S3 원본 경로 입력."
	msgWizardSourceHint   = "예: s3://my-bucket/deploy (sync) · s3://my-bucket/releases/api-1.2.3.tar.gz (tar)"
	msgWizardModeTitle    = "방식"
	msgWizardModePrompt   = "S3 원본을 어떻게 가져올지 선택."
	msgWizardSyncLabel    = "디렉터리 동기화 (sync, 삭제 동기화 없음)"
	msgWizardTarLabel     = "tar.gz 다운로드·검증·해제 (tar)"
	msgWizardDestTitle    = "받을 폴더"
	msgWizardDestPrompt   = "프로젝트 폴더 안에서 파일을 받을 폴더 입력."
	msgWizardDestHint     = "예: deploy · releases/api · assets (프로젝트 루트나 스크립트 폴더와 같을 수 없음)"
	msgWizardOutTitle     = "스크립트 경로"
	msgWizardOutPrompt    = "생성할 스크립트 파일 경로 입력."
	msgWizardOutHint      = "예: scripts/s3-sync.sh · scripts/fetch-assets.sh · deploy/get-release.sh"
)
