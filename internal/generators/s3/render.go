package s3

import (
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/generators"
)

type renderInput struct {
	layout  generators.Layout
	source  string
	mode    Mode
	profile string
}

// render produces the reviewed script. Every user-supplied value is passed
// through shQuote in an executable position; comments only display values that
// were already validated (no control characters, credentials or query strings).
func render(in renderInput) []byte {
	modeLabel := "디렉터리 동기화(sync)"
	if in.mode == ModeTar {
		modeLabel = "단일 tar.gz 다운로드·검증·해제(tar)"
	}
	projectDirExpr := ""
	if in.layout.ScriptInsideProject {
		rel := filepath.ToSlash(in.layout.ScriptToProject)
		if rel == "." {
			projectDirExpr = `PROJECT_DIR=$(CDPATH= cd -- "$SELF_DIR" && pwd -P)`
		} else {
			projectDirExpr = `PROJECT_DIR=$(CDPATH= cd -- "$SELF_DIR"/` + shQuote(rel) + ` && pwd -P)`
		}
	} else {
		projectDirExpr = "PROJECT_DIR=" + shQuote(in.layout.ProjectDir)
	}
	destExpr := "DEST=" + shQuote(in.layout.Destination)
	if !filepath.IsAbs(in.layout.DestinationRel) {
		destExpr = `DEST="$PROJECT_DIR"/` + shQuote(filepath.ToSlash(in.layout.DestinationRel))
	}
	scriptArg := in.layout.ScriptRel
	if scriptArg == "" {
		scriptArg = in.layout.ScriptPath
	}
	profileNote := "AWS 프로필: 지정하지 않음 (AWS CLI의 기본 자격 증명 확인 순서 사용)"
	if in.profile != "" {
		profileNote = "AWS 프로필: " + in.profile + " (기존 AWS CLI 구성의 프로필 이름, 비밀값 아님)"
	}

	body := syncScriptTemplate
	if in.mode == ModeTar {
		body = tarScriptTemplate
	}
	replacer := strings.NewReplacer(
		"{{SOURCE}}", in.source,
		"{{SOURCE_SH}}", shQuote(in.source),
		"{{MODE}}", string(in.mode),
		"{{MODE_LABEL}}", modeLabel,
		"{{DEST}}", in.layout.Destination,
		"{{PROJECT_DIR}}", in.layout.ProjectDir,
		"{{PROFILE}}", in.profile,
		"{{PROFILE_SH}}", shQuote(in.profile),
		"{{PROFILE_NOTE}}", profileNote,
		"{{PROJECT_DIR_EXPR}}", projectDirExpr,
		"{{DEST_EXPR}}", destExpr,
		"{{SCRIPT_ARG}}", shQuote(scriptArg),
	)
	return []byte(replacer.Replace(body))
}

// shQuote makes one POSIX single-quoted word. The generated script never
// interpolates user input into an unquoted position.
func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
