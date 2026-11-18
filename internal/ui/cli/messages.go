package cli

// Korean user-facing CLI text. Command names, flags and machine values stay
// English.
const (
	msgRootShort = "현재 폴더의 Docker Compose 스택을 돕는 도구"
	msgRootLong  = "dcraft는 현재 폴더의 Docker Compose 스택 상태를 조회합니다.\n" +
		"명령 없이 실행하면 대화형 화면을 열고, 명령을 지정하면 결과만 출력합니다."

	msgFlagHelp        = "도움말을 표시합니다."
	msgFlagJSON        = "기계 판독 가능한 JSON 결과를 출력합니다."
	msgFlagPath        = "대상 프로젝트 폴더 (기본: 현재 폴더)"
	msgFlagFile        = "사용할 Compose 파일 (순서대로, 반복 가능)"
	msgFlagEnvFile     = "Compose 환경 파일 (반복 가능)"
	msgFlagProfile     = "활성화할 Compose 프로필 (반복 가능)"
	msgFlagProjectName = "Compose 프로젝트 이름"
	msgFlagApprove     = "명시적 승인 (install, script_trust, previous_version, data_restore, file_overwrite)"
	msgFlagService     = "대상 Compose 서비스 (반복 가능)"
	msgHelpShort       = "명령 도움말을 표시합니다."

	msgNonInteractive = "dcraft: 대화형 터미널이 아니어서 화면을 열지 않았습니다.\n" +
		"다음처럼 명령을 지정해 실행하세요:\n"
	msgNonInteractiveJSON = "기계 판독 결과가 필요하면 --json을 추가하세요.\n"
	msgHelpHint           = "도움말: dcraft --help\n"
	msgJSONNeedsCommand   = "오류: --json을 사용할 때는 명령을 지정해야 합니다. 예: dcraft status --json\n도움말: dcraft --help\n"
)

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}사용법:
  {{.UseLine}}{{if .HasAvailableSubCommands}}

사용 가능한 명령:
{{range .Commands}}{{if .IsAvailableCommand}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

플래그:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

공통 플래그:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}

도움말: dcraft --help
`
