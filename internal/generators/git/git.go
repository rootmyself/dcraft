// Package git renders the reviewed, non-destructive Git host script produced
// by `dcraft generate-git-hook`. It never contacts a remote: all ref decisions
// that need the remote (a bare short name being a branch or a tag) are made by
// the generated script when the user runs it, not during generation.
package git

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rootmyself/dcraft/internal/generators"
)

// Kind is how a ref is resolved. Auto means the generated script asks the
// remote whether the short name is a branch or a tag.
type Kind string

const (
	KindAuto   Kind = "auto"
	KindBranch Kind = "branch"
	KindTag    Kind = "tag"
	KindCommit Kind = "commit"
)

// Input is one generation request. Layout is already validated by
// generators.ResolveLayout; Repository, Ref and Stage are validated here.
type Input struct {
	Layout     generators.Layout
	Repository string
	Ref        string
	Stage      string
}

// Generated is the rendered script and its identity. Content is UTF-8 without
// a BOM and ends with a newline; SHA256 is the content digest the hook trust
// record will later show.
type Generated struct {
	Layout     generators.Layout
	Repository string
	Ref        string
	RefShort   string
	Kind       Kind
	Stage      string
	Content    []byte
	SHA256     string
	Existed    bool
}

var (
	fullSHARe  = regexp.MustCompile(`^[0-9a-fA-F]{40}$|^[0-9a-fA-F]{64}$`)
	shortSHARe = regexp.MustCompile(`^[0-9a-fA-F]{4,39}$`)
)

// ClassifyRef maps an explicit ref spelling to a script variant. A bare name
// stays KindAuto because generation must not contact the remote; the generated
// script disambiguates a branch from a tag at execution time.
func ClassifyRef(raw string) (Kind, string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return "", "", inputError("ref", "ref(브랜치·태그·전체 SHA)를 입력하세요.", "")
	}
	if hasControl(ref) {
		return "", "", inputError("ref", "ref에 허용되지 않는 제어 문자가 있습니다.", "")
	}
	if strings.HasPrefix(ref, "-") {
		return "", "", inputError("ref", "ref는 -로 시작할 수 없습니다.", ref)
	}
	if strings.EqualFold(ref, "HEAD") {
		return "", "", inputError("ref", "HEAD 대신 branch, tag 또는 전체 SHA를 지정하세요.", ref)
	}
	switch {
	case fullSHARe.MatchString(ref):
		return KindCommit, strings.ToLower(ref), nil
	case strings.HasPrefix(ref, "refs/heads/"):
		short := strings.TrimPrefix(ref, "refs/heads/")
		if err := validateShortRef("branch", short); err != nil {
			return "", "", err
		}
		return KindBranch, short, nil
	case strings.HasPrefix(ref, "refs/tags/"):
		short := strings.TrimPrefix(ref, "refs/tags/")
		if err := validateShortRef("tag", short); err != nil {
			return "", "", err
		}
		return KindTag, short, nil
	case strings.HasPrefix(ref, "refs/"):
		return "", "", inputError("ref", "지원하지 않는 전체 ref 형식입니다. refs/heads/... 또는 refs/tags/...를 사용하세요.", ref)
	case shortSHARe.MatchString(ref):
		return "", "", inputError("ref", "짧은 SHA는 지원하지 않습니다. 40자(또는 64자) 전체 SHA를 입력하세요.", ref)
	default:
		if err := validateShortRef("auto", ref); err != nil {
			return "", "", err
		}
		return KindAuto, ref, nil
	}
}

func validateShortRef(kind, ref string) error {
	if ref == "" {
		return inputError("ref", "ref 이름이 비어 있습니다.", "")
	}
	if strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") || strings.Contains(ref, "//") {
		return inputError("ref", "ref 이름의 / 위치가 올바르지 않습니다.", ref)
	}
	if strings.HasSuffix(ref, ".") {
		return inputError("ref", "ref 이름은 .로 끝날 수 없습니다.", ref)
	}
	if strings.Contains(ref, "..") {
		return inputError("ref", "ref 이름에 ..를 사용할 수 없습니다.", ref)
	}
	if strings.ContainsAny(ref, " ~^:?*[\\\t") || strings.Contains(ref, "@{") {
		return inputError("ref", "ref 이름에 Git이 허용하지 않는 문자가 있습니다.", ref)
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return inputError("ref", "ref 이름의 경로 구성 요소가 올바르지 않습니다.", ref)
		}
	}
	if kind != "auto" {
		// A conservative local check; the generated script runs
		// `git check-ref-format` again before touching the destination.
	}
	return nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

// Build validates all generation inputs, resolves the project-scoped paths and
// renders the script. It performs no network access and writes no file.
func Build(in Input) (Generated, error) {
	repo := strings.TrimSpace(in.Repository)
	if repo == "" {
		return Generated{}, inputError("repo", "저장소 URL 또는 경로를 입력하세요.", "")
	}
	if hasControl(repo) {
		return Generated{}, inputError("repo", "저장소 값에 허용되지 않는 제어 문자가 있습니다.", "")
	}
	if strings.HasPrefix(repo, "-") {
		return Generated{}, inputError("repo", "저장소 값은 -로 시작할 수 없습니다.", repo)
	}
	if credentialsInRepo(repo) {
		return Generated{}, inputError("repo",
			"저장소 URL에 인증 정보가 들어 있습니다. 기존 credential helper 또는 SSH agent를 사용하고 URL에서는 인증 정보를 빼세요.", repo)
	}
	kind, short, err := ClassifyRef(in.Ref)
	if err != nil {
		return Generated{}, err
	}
	stage := strings.TrimSpace(in.Stage)
	if stage != "pre" && stage != "post" {
		return Generated{}, inputError("stage", "실행 시점은 pre 또는 post여야 합니다.", stage)
	}
	if in.Layout.ProjectDir == "" || in.Layout.Destination == "" || in.Layout.ScriptPath == "" {
		return Generated{}, inputError("layout", "프로젝트·받을 폴더·스크립트 경로가 모두 필요합니다.", "")
	}

	content := render(renderInput{
		layout: in.Layout,
		repo:   repo,
		ref:    short,
		kind:   kind,
		stage:  stage,
	})
	sum := sha256.Sum256(content)
	existed := false
	if info, statErr := os.Lstat(in.Layout.ScriptPath); statErr == nil {
		existed = true
		if info.IsDir() || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return Generated{}, inputError("output", "스크립트 경로가 일반 파일이 아닙니다. 안전을 위해 덮어쓰지 않습니다.", in.Layout.ScriptPath)
		}
	} else if !os.IsNotExist(statErr) {
		return Generated{}, statErr
	}
	return Generated{
		Layout:     in.Layout,
		Repository: repo,
		Ref:        in.Ref,
		RefShort:   short,
		Kind:       kind,
		Stage:      stage,
		Content:    content,
		SHA256:     hex.EncodeToString(sum[:]),
		Existed:    existed,
	}, nil
}

func inputError(field, message, detail string) *generators.InputError {
	return &generators.InputError{Field: field, Message: message, Detail: detail}
}

// credentialsInRepo rejects userinfo that could be stored on disk:
// any userinfo on http(s) (often a token) and a password in another scheme.
// A plain SSH username (`git@host`, `ssh://git@host`) is not a credential.
func credentialsInRepo(repo string) bool {
	schemeEnd := strings.Index(repo, "://")
	if schemeEnd < 0 {
		return false
	}
	scheme := strings.ToLower(repo[:schemeEnd])
	rest := repo[schemeEnd+3:]
	authority := rest
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		authority = rest[:cut]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return false
	}
	userinfo := authority[:at]
	if scheme == "http" || scheme == "https" {
		return true
	}
	return strings.Contains(userinfo, ":")
}

type renderInput struct {
	layout generators.Layout
	repo   string
	ref    string
	kind   Kind
	stage  string
}

func render(in renderInput) []byte {
	stageLabel := "사전(pre) — up/update 전에 실행"
	if in.stage == "post" {
		stageLabel = "사후(post) — up/update 완료 후 실행"
	}
	kindLabel := "자동 판별(branch/tag)"
	switch in.kind {
	case KindBranch:
		kindLabel = "branch"
	case KindTag:
		kindLabel = "tag (태그, annotated tag는 commit으로 peel)"
	case KindCommit:
		kindLabel = "전체 SHA(commit)"
	}
	projectDirExpr := ""
	if in.layout.ScriptInsideProject {
		rel := filepathSlash(in.layout.ScriptToProject)
		if rel == "." {
			projectDirExpr = `PROJECT_DIR=$(CDPATH= cd -- "$SELF_DIR" && pwd -P)`
		} else {
			projectDirExpr = `PROJECT_DIR=$(CDPATH= cd -- "$SELF_DIR"/` + shQuote(rel) + ` && pwd -P)`
		}
	} else {
		projectDirExpr = "PROJECT_DIR=" + shQuote(in.layout.ProjectDir)
	}
	destExpr := "DEST=" + shQuote(in.layout.Destination)
	if insideProject(in.layout) {
		destExpr = `DEST="$PROJECT_DIR"/` + shQuote(filepathSlash(in.layout.DestinationRel))
	}
	scriptArg := in.layout.ScriptRel
	if scriptArg == "" {
		scriptArg = in.layout.ScriptPath
	}

	script := gitScriptTemplate
	replacer := strings.NewReplacer(
		"{{REPO}}", in.repo,
		"{{REF}}", in.ref,
		"{{KIND_LABEL}}", kindLabel,
		"{{STAGE_LABEL}}", stageLabel,
		"{{PROJECT_DIR}}", in.layout.ProjectDir,
		"{{DEST}}", in.layout.Destination,
		"{{REPO_SH}}", shQuote(in.repo),
		"{{REF_SH}}", shQuote(in.ref),
		"{{KIND_SH}}", shQuote(string(in.kind)),
		"{{PROJECT_DIR_EXPR}}", projectDirExpr,
		"{{DEST_EXPR}}", destExpr,
		"{{STAGE}}", in.stage,
		"{{SCRIPT_ARG}}", shQuote(scriptArg),
	)
	return []byte(replacer.Replace(script))
}

func insideProject(layout generators.Layout) bool {
	return !filepath.IsAbs(layout.DestinationRel)
}

func filepathSlash(path string) string { return filepath.ToSlash(path) }

// shQuote makes one POSIX single-quoted word. The generated script never
// interpolates user input into an unquoted position.
func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
