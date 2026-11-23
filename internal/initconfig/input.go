package initconfig

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// Mode is the two ways a first Compose file can be created.
type Mode string

const (
	ModeDockerfile Mode = "dockerfile"
	ModeImage      Mode = "image"
)

// Input is the parsed, still-unresolved user input. Paths are relative to the
// project folder unless absolute; nothing here has been inferred.
type Input struct {
	Mode       Mode
	Service    string
	Output     string
	Context    string
	Dockerfile string
	Image      string
	Ports      []string
	Volumes    []string
	Env        []string
	Command    string
	Restart    string
	wizard     bool
}

func (i Input) modeLabel() string {
	if i.Mode == ModeDockerfile {
		return "Dockerfile"
	}
	return "이미지"
}

func inputErr(format string, args ...any) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryInitInputInvalid,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(format, args...),
		NextActions: []string{"dcraft init --help로 필요한 입력을 확인하세요.", "값을 추측하지 않으며 누락된 입력이 있으면 아무 파일도 만들지 않습니다."},
	})
}

func inputErrDetail(msg, detail string, actions ...string) *core.Error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryInitInputInvalid,
		Severity:    core.SeverityError,
		Message:     msg,
		Detail:      detail,
		NextActions: append([]string{}, actions...),
	})
}

// collectInput maps the feature flags or wizard answers to Input and rejects
// missing or contradictory values. It never reads a Dockerfile, image or port.
func collectInput(in core.Invocation, wizard bool) (Input, error) {
	raw := Input{
		Mode:       Mode(strings.TrimSpace(in.Option("from"))),
		Service:    strings.TrimSpace(in.Option("service-name")),
		Output:     strings.TrimSpace(in.Option("output")),
		Context:    strings.TrimSpace(in.Option("context")),
		Dockerfile: strings.TrimSpace(in.Option("dockerfile")),
		Image:      strings.TrimSpace(in.Option("image")),
		Ports:      optionValues(in, "port", wizard),
		Volumes:    optionValues(in, "volume", wizard),
		Env:        optionValues(in, "env-var", wizard),
		Command:    strings.TrimSpace(in.Option("command")),
		Restart:    strings.TrimSpace(in.Option("restart")),
		wizard:     wizard,
	}

	switch raw.Mode {
	case ModeDockerfile:
		if raw.Context == "" {
			raw.Context = "."
		}
		if raw.Dockerfile == "" {
			raw.Dockerfile = "Dockerfile"
		}
		if raw.Image != "" {
			if !wizard {
				return Input{}, inputErr(msgDockerfileMode)
			}
			raw.Image = ""
		}
	case ModeImage:
		if raw.Context != "" || raw.Dockerfile != "" {
			if !wizard {
				return Input{}, inputErr(msgImageModeOnly)
			}
			raw.Context = ""
			raw.Dockerfile = ""
		}
	case "":
		return Input{}, inputErr(msgModeMissing + " (--from dockerfile|image)")
	default:
		return Input{}, inputErr("%s (--from, 받은 값: %s)", msgModeInvalid, raw.Mode)
	}

	if raw.Service == "" {
		return Input{}, inputErr(msgServiceMissing + " (--service-name)")
	}
	if err := validateServiceName(raw.Service); err != nil {
		return Input{}, err
	}
	if raw.Mode == ModeImage {
		if raw.Image == "" {
			return Input{}, inputErr(msgImageMissing + " (--image)")
		}
		if err := validateImageRef(raw.Image); err != nil {
			return Input{}, err
		}
	}
	for _, port := range raw.Ports {
		if err := validatePort(port); err != nil {
			return Input{}, err
		}
	}
	for _, volume := range raw.Volumes {
		if _, err := parseVolume(volume); err != nil {
			return Input{}, err
		}
	}
	if _, err := parseEnvironment(raw.Env); err != nil {
		return Input{}, err
	}
	if err := validateRestart(raw.Restart); err != nil {
		return Input{}, err
	}
	return raw, nil
}

// optionValues preserves repeatable flags and decodes wizard answers by line.
func optionValues(in core.Invocation, name string, wizard bool) []string {
	if list := in.OptionList(name); len(list) > 0 {
		out := make([]string, 0, len(list))
		for _, value := range list {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}
	if wizard {
		var values []string
		for _, value := range strings.Split(in.Option(name), "\n") {
			if value = strings.TrimSpace(value); value != "" {
				values = append(values, value)
			}
		}
		return values
	}
	return splitComma(in.Option(name))
}

func splitComma(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

var serviceNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// validateServiceName mirrors Compose's service-key schema. It rejects empty
// names and separators Compose refuses (spaces, slashes, colons, @).
func validateServiceName(name string) error {
	if name == "" {
		return inputErr(msgServiceMissing)
	}
	if !serviceNameRe.MatchString(name) {
		return inputErrDetail(msgServiceInvalid, fmt.Sprintf("서비스 이름: %q (영문·숫자·_ . -만 사용할 수 있습니다)", name))
	}
	return nil
}

var (
	imagePathComponentRe = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*$`)
	imageTagRe           = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	imageDomainRe        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*(?::[0-9]+)?$`)
	imageDigestRe        = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*:[0-9a-fA-F]{32,}$`)
)

// validateImageRef checks Docker's reference grammar without pulling. Compose
// config does not validate image references, so dcraft must reject syntax that
// Docker would refuse later.
func validateImageRef(ref string) error {
	fail := func(reason string) error {
		return inputErrDetail(msgImageInvalid, fmt.Sprintf("이미지 참조: %q (%s)", ref, reason))
	}
	if ref == "" {
		return inputErr(msgImageMissing)
	}
	if strings.TrimSpace(ref) != ref || strings.ContainsAny(ref, " \t\r\n") {
		return fail("공백을 포함할 수 없습니다")
	}
	if strings.Contains(ref, "://") || strings.Contains(ref, "$") {
		return fail("주소나 변수 대신 레지스트리/저장소 참조를 입력하세요")
	}
	nameTag := ref
	if strings.Count(ref, "@") > 1 {
		return fail("@는 한 번만 사용할 수 있습니다")
	}
	if at := strings.Index(ref, "@"); at >= 0 {
		nameTag = ref[:at]
		digest := ref[at+1:]
		if !validImageDigest(digest) {
			return fail("digest 형식이 올바르지 않습니다")
		}
	}
	if nameTag == "" {
		return fail("저장소 이름이 없습니다")
	}
	if colon := strings.LastIndex(nameTag, ":"); colon > strings.LastIndex(nameTag, "/") {
		tag := nameTag[colon+1:]
		if !imageTagRe.MatchString(tag) {
			return fail("tag 형식이 올바르지 않습니다")
		}
		nameTag = nameTag[:colon]
	}
	parts := strings.Split(nameTag, "/")
	if len(parts) == 0 {
		return fail("저장소 이름이 없습니다")
	}
	for _, part := range parts {
		if part == "" {
			return fail("빈 경로 요소가 있습니다")
		}
	}
	if len(parts) > 1 && imageDomainPart(parts[0]) {
		if !imageDomainRe.MatchString(parts[0]) {
			return fail("레지스트리 주소 형식이 올바르지 않습니다")
		}
		if host, port, ok := strings.Cut(parts[0], ":"); ok {
			if strings.Contains(host, ":") || !validPortNumber(port) {
				return fail("레지스트리 포트가 올바르지 않습니다")
			}
		}
		parts = parts[1:]
	}
	for _, part := range parts {
		if !imagePathComponentRe.MatchString(part) {
			return fail("저장소 경로는 소문자/숫자와 _ . - 구분자만 사용할 수 있습니다")
		}
	}
	return nil
}

func imageDomainPart(part string) bool {
	return strings.ContainsAny(part, ".:") || strings.EqualFold(part, "localhost")
}

// validImageDigest enforces the exact hex length of the common algorithms.
// The reference grammar only requires 32+ hex, which would accept a truncated
// sha256 digest that Docker later fails to parse or verify.
func validImageDigest(digest string) bool {
	if !imageDigestRe.MatchString(digest) {
		return false
	}
	algorithm, hex, ok := strings.Cut(digest, ":")
	if !ok {
		return false
	}
	switch algorithm {
	case "sha256":
		return len(hex) == 64
	case "sha512":
		return len(hex) == 128
	default:
		return len(hex) >= 32
	}
}

func validPortNumber(raw string) bool {
	n, err := strconv.Atoi(raw)
	return err == nil && n >= 1 && n <= 65535
}

var portRangeRe = regexp.MustCompile(`^([0-9]{1,5})(?:-([0-9]{1,5}))?$`)

type portRange struct {
	start int
	end   int
}

func (r portRange) isRange() bool { return r.start != r.end }

// validatePort accepts Compose's short port syntax only:
//
//	CONTAINER[/PROTO]
//	HOST:CONTAINER[/PROTO]
//	IP:HOST:CONTAINER[/PROTO]
//
// The host port may be empty (IP::CONTAINER) for a random published port.
// Long-syntax port mappings cannot be expressed through this flag.
func validatePort(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return inputErr(msgPortInvalid + ": 빈 값")
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (공백을 포함할 수 없습니다)", raw))
	}
	hostIP := ""
	rest := s
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 || end == 1 {
			return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (IPv6 주소는 [주소] 형식으로 닫아야 합니다)", raw))
		}
		hostIP = s[1:end]
		if net.ParseIP(hostIP) == nil {
			return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (호스트 IP가 올바르지 않습니다)", raw))
		}
		rest = s[end+1:]
		if !strings.HasPrefix(rest, ":") {
			return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (IPv6 주소 뒤에 :가 필요합니다)", raw))
		}
		rest = rest[1:]
		parts := strings.Split(rest, ":")
		if len(parts) != 2 {
			return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q ([IPv6]:호스트:컨테이너 형식이어야 합니다)", raw))
		}
		return validatePortParts(parts[0], parts[1], true, raw)
	}

	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		return validateContainerPart(parts[0], raw)
	case 2:
		return validatePortParts(parts[0], parts[1], false, raw)
	case 3:
		if net.ParseIP(parts[0]) == nil {
			return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (호스트 IP가 올바르지 않습니다)", raw))
		}
		return validatePortParts(parts[1], parts[2], true, raw)
	default:
		return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (호스트 IP를 쓰려면 127.0.0.1:8080:80 형식으로 지정하세요)", raw))
	}
}

func validateContainerPart(container, raw string) error {
	portPart, proto, err := splitPortProto(container, raw)
	if err != nil {
		return err
	}
	if _, err := parsePortRange(portPart, raw); err != nil {
		return err
	}
	_ = proto
	return nil
}

func validatePortParts(host, container string, allowEmptyHost bool, raw string) error {
	if host == "" && !allowEmptyHost {
		return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (호스트 포트가 비어 있습니다)", raw))
	}
	containerPart, proto, err := splitPortProto(container, raw)
	if err != nil {
		return err
	}
	cRange, err := parsePortRange(containerPart, raw)
	if err != nil {
		return err
	}
	if host == "" {
		return nil
	}
	hRange, err := parsePortRange(host, raw)
	if err != nil {
		return err
	}
	if hRange.isRange() != cRange.isRange() {
		return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (호스트와 컨테이너 포트는 둘 다 범위이거나 둘 다 단일 값이어야 합니다)", raw))
	}
	if hRange.isRange() && (hRange.end-hRange.start) != (cRange.end-cRange.start) {
		return inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (호스트와 컨테이너 포트 범위 길이가 같아야 합니다)", raw))
	}
	_ = proto
	return nil
}

func splitPortProto(container, raw string) (string, string, error) {
	parts := strings.Split(container, "/")
	if len(parts) > 2 {
		return "", "", inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (프로토콜은 한 번만 지정할 수 있습니다)", raw))
	}
	proto := "tcp"
	if len(parts) == 2 {
		proto = parts[1]
		if proto != "tcp" && proto != "udp" {
			return "", "", inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (프로토콜은 tcp 또는 udp만 사용할 수 있습니다)", raw))
		}
	}
	return parts[0], proto, nil
}

func parsePortRange(raw, full string) (portRange, error) {
	m := portRangeRe.FindStringSubmatch(raw)
	if m == nil {
		return portRange{}, inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (숫자 또는 시작-끝 범위가 필요합니다)", full))
	}
	start, err := strconv.Atoi(m[1])
	if err != nil || start < 1 || start > 65535 {
		return portRange{}, inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (포트는 1-65535 범위여야 합니다)", full))
	}
	end := start
	if m[2] != "" {
		end, err = strconv.Atoi(m[2])
		if err != nil || end < 1 || end > 65535 || end < start {
			return portRange{}, inputErrDetail(msgPortInvalid, fmt.Sprintf("포트: %q (끝 포트가 올바르지 않습니다)", full))
		}
	}
	return portRange{start: start, end: end}, nil
}

// volumeSpec is one parsed short-syntax mount. Source is empty for an
// anonymous volume.
type volumeSpec struct {
	Source string
	Target string
	Mode   string
	Named  bool
	Bind   bool
	Home   bool
	Raw    string
}

var namedVolumeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

var volumeModes = map[string]bool{
	"ro": true, "rw": true, "z": true, "Z": true,
	"cached": true, "delegated": true, "consistent": true,
	"nocopy": true, "shared": true, "slave": true, "private": true,
	"rshared": true, "rslave": true, "rprivate": true,
}

func parseVolume(raw string) (volumeSpec, error) {
	fail := func(reason string) (volumeSpec, error) {
		return volumeSpec{}, inputErrDetail(msgVolumeInvalid, fmt.Sprintf("볼륨: %q (%s)", raw, reason))
	}
	s := strings.TrimSpace(raw)
	if s == "" {
		return fail("빈 값")
	}
	if strings.Contains(s, "$") {
		return fail("변수 대신 실제 경로나 볼륨 이름을 입력하세요")
	}
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return fail("SOURCE:TARGET[:MODE] 형식이어야 합니다")
	}
	spec := volumeSpec{Raw: s}
	switch len(parts) {
	case 1:
		spec.Target = parts[0]
	case 2:
		spec.Source, spec.Target = parts[0], parts[1]
	case 3:
		spec.Source, spec.Target, spec.Mode = parts[0], parts[1], parts[2]
	}
	if len(parts) > 1 && spec.Source == "" {
		return fail("원본이 비어 있습니다")
	}
	if spec.Target == "" || !strings.HasPrefix(spec.Target, "/") {
		return fail("대상 경로는 /로 시작하는 절대 경로여야 합니다")
	}
	if hasDotDot(spec.Target) {
		return fail("대상 경로에 ..를 사용할 수 없습니다")
	}
	if spec.Mode != "" {
		for _, mode := range strings.Split(spec.Mode, ",") {
			if !volumeModes[mode] {
				return fail(fmt.Sprintf("지원하지 않는 모드입니다: %s", mode))
			}
		}
	}
	if spec.Source == "" {
		return spec, nil
	}
	switch {
	case strings.HasPrefix(spec.Source, "/"):
		spec.Bind = true
	case strings.HasPrefix(spec.Source, "."):
		spec.Bind = true
	case strings.HasPrefix(spec.Source, "~"):
		spec.Bind = true
		spec.Home = true
	default:
		if !namedVolumeRe.MatchString(spec.Source) {
			return fail("볼륨 이름은 영문·숫자로 시작하고 _ . -만 사용할 수 있습니다")
		}
		spec.Named = true
	}
	return spec, nil
}

func hasDotDot(target string) bool {
	for _, part := range strings.Split(target, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseEnvironment accepts only explicit KEY=VALUE entries. A bare KEY would
// import the host's value, which dcraft never does implicitly.
func parseEnvironment(entries []string) (map[string]string, error) {
	out := make(map[string]string, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		eq := strings.Index(entry, "=")
		if eq <= 0 {
			// Never echo the raw entry: a malformed line can contain a value
			// the user intended as a secret.
			return nil, inputErrDetail(msgEnvInvalid, "환경변수 항목이 KEY=VALUE 형식이 아닙니다. 호스트 값을 자동으로 가져오지 않으며 입력값은 출력하지 않았습니다.")
		}
		key := entry[:eq]
		value := entry[eq+1:]
		if !envKeyRe.MatchString(key) {
			return nil, inputErrDetail(msgEnvInvalid, fmt.Sprintf("환경변수 이름: %q (영문·숫자·_만 사용할 수 있고 숫자로 시작할 수 없습니다)", key))
		}
		if _, exists := out[key]; exists {
			return nil, inputErrDetail(msgEnvInvalid, fmt.Sprintf("환경변수 이름이 중복되었습니다: %s", key))
		}
		out[key] = value
	}
	return out, nil
}

var onFailureRe = regexp.MustCompile(`^on-failure:([1-9][0-9]*)$`)

func validateRestart(raw string) error {
	value := strings.TrimSpace(raw)
	switch value {
	case "", "no", "always", "unless-stopped", "on-failure":
		return nil
	}
	if onFailureRe.MatchString(value) {
		return nil
	}
	return inputErrDetail(msgRestartInvalid, fmt.Sprintf("restart: %q (no, always, unless-stopped, on-failure, on-failure:횟수만 사용할 수 있습니다)", raw))
}

// Destination is the resolved save target. Existing is true for any non-directory
// path (a regular file or a special file); Exists is used by the approval check.
type Destination struct {
	Path     string
	Dir      string
	Explicit bool
	Outside  bool
	Exists   bool
}

func destinationPath(projectDir, raw string) (Destination, error) {
	value := strings.TrimSpace(raw)
	dest := Destination{Explicit: value != ""}
	if value == "" {
		value = "compose.yaml"
	}
	if value == "-" {
		return Destination{}, inputErrDetail(msgOutputInvalid, "표준 출력으로 저장할 수 없습니다. 파일 경로를 지정하세요.")
	}
	if strings.HasPrefix(value, "~") {
		return Destination{}, inputErrDetail(msgOutputInvalid, "~ 경로는 지원하지 않습니다. 상대 경로나 절대 경로를 지정하세요.")
	}
	if strings.Contains(value, "$") {
		return Destination{}, inputErrDetail(msgOutputInvalid, "변수 대신 실제 경로를 입력하세요.")
	}
	if strings.ContainsRune(value, 0) {
		return Destination{}, inputErrDetail(msgOutputInvalid, "경로에 NUL 문자를 사용할 수 없습니다.")
	}
	p := value
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectDir, p)
	}
	p = filepath.Clean(p)
	dest.Path = p
	dest.Dir = filepath.Dir(p)

	info, err := os.Stat(dest.Dir)
	if err != nil {
		return Destination{}, inputErrDetail(msgOutputParent, fmt.Sprintf("저장 폴더: %s", dest.Dir))
	}
	if !info.IsDir() {
		return Destination{}, inputErrDetail(msgOutputParent, fmt.Sprintf("저장 폴더가 아닙니다: %s", dest.Dir))
	}
	if linkInfo, err := os.Lstat(p); err == nil {
		dest.Exists = true
		if linkInfo.Mode()&os.ModeSymlink != 0 {
			return Destination{}, inputErrDetail(msgOutputSymlink, p)
		}
		if linkInfo.IsDir() {
			return Destination{}, inputErrDetail(msgOutputDir, p)
		}
		if !linkInfo.Mode().IsRegular() {
			return Destination{}, inputErrDetail(msgOutputSpecial, p)
		}
	} else if !os.IsNotExist(err) {
		return Destination{}, inputErrDetail(msgOutputInvalid, fmt.Sprintf("%s: %v", p, err))
	}
	dest.Outside = !insideProject(projectDir, p)
	return dest, nil
}

// insideProject compares both the literal and the symlink-resolved spellings so
// a symlinked temp folder or project path is not misreported as outside.
func insideProject(projectDir, target string) bool {
	if rel, err := filepath.Rel(projectDir, target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return true
	}
	realProject, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return false
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realProject, filepath.Join(realParent, filepath.Base(target)))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var standardComposeFiles = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}

// existingComposeFiles lists standard files in Compose's preference order plus
// any explicit target files, without duplicates. It never writes.
func existingComposeFiles(projectDir string, targetFiles []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		clean := filepath.Clean(p)
		if seen[clean] {
			return
		}
		seen[clean] = true
		out = append(out, clean)
	}
	for _, name := range standardComposeFiles {
		p := filepath.Join(projectDir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			add(p)
		}
	}
	for _, p := range targetFiles {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			add(p)
		}
	}
	return out
}

// overrideComposeFiles lists standard override files even when their base file
// is absent. Creating a base next to an existing override activates the
// override through normal Compose discovery, so the preview must say so.
func overrideComposeFiles(projectDir string) []string {
	var out []string
	for _, base := range standardComposeFiles {
		ext := filepath.Ext(base)
		name := strings.TrimSuffix(base, ext) + ".override" + ext
		p := filepath.Join(projectDir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

// sameFile compares cleaned paths and, when both exist, the OS identity. It
// never follows a symlink for the destination (the destination is refused
// before this point when it is one).
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}
