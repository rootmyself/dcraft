// Package s3 renders the reviewed, non-destructive S3 host scripts produced by
// `dcraft generate-s3-hook`. It never contacts S3 and never invokes the AWS CLI:
// every download decision is made by the generated script when the user runs it.
package s3

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/rootmyself/dcraft/internal/generators"
)

// Mode selects the generated script behavior.
type Mode string

const (
	// ModeSync mirrors an S3 prefix into the destination without deleting
	// files that exist only locally.
	ModeSync Mode = "sync"
	// ModeTar downloads one .tar.gz object, verifies and validates it, then
	// extracts it into a fresh temporary folder before copying.
	ModeTar Mode = "tar"
)

// Input is one generation request. Layout is already validated by
// generators.ResolveLayout; Source, Mode and Profile are validated here.
type Input struct {
	Layout  generators.Layout
	Source  string
	Mode    Mode
	Profile string
}

// Generated is the rendered script and its identity. Content is UTF-8 without
// a BOM and ends with a newline; SHA256 is the content digest the hook trust
// record will later show.
type Generated struct {
	Layout  generators.Layout
	Source  string
	Mode    Mode
	Profile string
	Content []byte
	SHA256  string
	Existed bool
}

var (
	bucketRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*[A-Za-z0-9]$`)
	profileRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]*$`)
)

// Build validates all generation inputs and renders the script. It performs no
// network access, runs no AWS CLI command and writes no file.
func Build(in Input) (Generated, error) {
	source, err := normalizeSource(in.Source, in.Mode)
	if err != nil {
		return Generated{}, err
	}
	profile, err := normalizeProfile(in.Profile)
	if err != nil {
		return Generated{}, err
	}
	if in.Layout.ProjectDir == "" || in.Layout.Destination == "" || in.Layout.ScriptPath == "" {
		return Generated{}, inputError("layout", "프로젝트·받을 폴더·스크립트 경로가 모두 필요합니다.", "")
	}

	content := render(renderInput{layout: in.Layout, source: source, mode: in.Mode, profile: profile})
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
		Layout:  in.Layout,
		Source:  source,
		Mode:    in.Mode,
		Profile: profile,
		Content: content,
		SHA256:  hex.EncodeToString(sum[:]),
		Existed: existed,
	}, nil
}

func normalizeSource(raw string, mode Mode) (string, error) {
	source := strings.TrimSpace(raw)
	if source == "" {
		return "", inputError("source", "S3 원본 주소를 입력하세요.", "")
	}
	if mode != ModeSync && mode != ModeTar {
		return "", inputError("mode", "방식은 sync 또는 tar여야 합니다.", "")
	}
	if hasControl(source) || strings.IndexFunc(source, unicode.IsSpace) >= 0 {
		return "", inputError("source", "S3 원본에 공백·제어 문자를 사용할 수 없습니다.", "")
	}
	if !strings.HasPrefix(source, "s3://") {
		return "", inputError("source", "S3 원본은 s3://bucket/prefix 형식이어야 합니다.", "")
	}
	rest := strings.TrimPrefix(source, "s3://")
	authority := rest
	key := ""
	if cut := strings.IndexByte(rest, '/'); cut >= 0 {
		authority = rest[:cut]
		key = rest[cut+1:]
	}
	if authority == "" {
		return "", inputError("source", "S3 원본에 버킷 이름이 없습니다.", "")
	}
	if strings.Contains(authority, "@") {
		return "", inputError("source", "S3 주소에는 인증 정보를 넣을 수 없습니다. 기존 AWS CLI 자격 증명을 사용하세요.", "")
	}
	if !bucketRe.MatchString(authority) || len(authority) < 3 || len(authority) > 63 || strings.Contains(authority, "..") {
		return "", inputError("source", "S3 버킷 이름 형식이 올바르지 않습니다.", "")
	}
	if strings.ContainsAny(source, "?#") {
		return "", inputError("source", "쿼리 문자열·미리 서명된 URL은 지원하지 않습니다. 기존 AWS CLI 자격 증명을 사용하세요.", "")
	}
	if strings.ContainsAny(key, "\\*") {
		return "", inputError("source", "S3 경로에 \\ 또는 * 문자를 사용할 수 없습니다.", "")
	}
	if strings.HasPrefix(key, "/") {
		return "", inputError("source", "S3 경로에 빈 구성 요소를 사용할 수 없습니다. 버킷 뒤에는 하나의 /만 사용하세요.", "")
	}
	if key != "" {
		trimmed := key
		if mode == ModeSync && strings.HasSuffix(trimmed, "/") {
			trimmed = strings.TrimSuffix(trimmed, "/")
		}
		if trimmed == "" && mode == ModeTar {
			return "", inputError("source", "tar 방식에는 단일 .tar.gz 객체 경로가 필요합니다.", "")
		}
		if trimmed != "" {
			for _, part := range strings.Split(trimmed, "/") {
				if part == "" || part == "." || part == ".." {
					return "", inputError("source", "S3 경로의 구성 요소가 올바르지 않습니다. 빈 경로·.·..는 사용할 수 없습니다.", "")
				}
			}
		}
		key = trimmed
	}
	if mode == ModeSync {
		if strings.HasSuffix(key, ".tar.gz") {
			return "", inputError("source", "sync 방식에는 .tar.gz 객체를 지정할 수 없습니다. tar 방식을 사용하세요.", "")
		}
	} else {
		if !strings.HasSuffix(key, ".tar.gz") {
			return "", inputError("source", "tar 방식에는 .tar.gz 객체 경로가 필요합니다.", "")
		}
		if base := key[strings.LastIndex(key, "/")+1:]; base == ".tar.gz" {
			return "", inputError("source", "tar 방식에는 파일 이름이 있는 .tar.gz 객체가 필요합니다.", "")
		}
	}
	normalized := "s3://" + authority
	if key != "" {
		normalized += "/" + key
	}
	return normalized, nil
}

func normalizeProfile(raw string) (string, error) {
	profile := strings.TrimSpace(raw)
	if profile == "" {
		return "", nil
	}
	if hasControl(profile) || strings.IndexFunc(profile, unicode.IsSpace) >= 0 || !profileRe.MatchString(profile) {
		return "", inputError("aws-profile", "AWS 프로필 이름은 영문·숫자와 . _ @ + - 만 사용할 수 있습니다.", "")
	}
	return profile, nil
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func inputError(field, message, detail string) *generators.InputError {
	return &generators.InputError{Field: field, Message: message, Detail: detail}
}
