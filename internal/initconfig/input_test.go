package initconfig

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func invocation(options map[string]string, lists map[string][]string) core.Invocation {
	return core.Invocation{
		Target:      core.Target{ProjectDir: "/project"},
		Options:     options,
		OptionLists: lists,
	}
}

func assertInputError(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an input error containing %q", contains)
	}
	ce, ok := err.(*core.Error)
	if !ok || len(ce.Diagnostics) == 0 {
		t.Fatalf("error = %T %v, want *core.Error", err, err)
	}
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryInitInputInvalid {
		t.Fatalf("category = %q, want init_input_invalid", d.Category)
	}
	if !strings.Contains(d.Message+d.Detail, contains) {
		t.Fatalf("diagnostic = %+v, want %q", d, contains)
	}
}

func TestCollectRequiresTheModeAndServiceName(t *testing.T) {
	_, err := collectInput(invocation(map[string]string{}, nil), false)
	assertInputError(t, err, "--from")

	_, err = collectInput(invocation(map[string]string{"from": "image"}, nil), false)
	assertInputError(t, err, "--service-name")

	_, err = collectInput(invocation(map[string]string{"from": "guess", "service-name": "app"}, nil), false)
	assertInputError(t, err, "--from")

	input, err := collectInput(invocation(map[string]string{
		"from": "dockerfile", "service-name": "app",
	}, nil), false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if input.Mode != ModeDockerfile || input.Service != "app" {
		t.Fatalf("input = %+v", input)
	}
	if input.Context != "." || input.Dockerfile != "Dockerfile" {
		t.Fatalf("Dockerfile defaults = %q/%q, want . and Dockerfile", input.Context, input.Dockerfile)
	}
}

func TestCollectModeSpecificInputsAreNotInvented(t *testing.T) {
	_, err := collectInput(invocation(map[string]string{
		"from": "image", "service-name": "web",
	}, nil), false)
	assertInputError(t, err, "--image")

	_, err = collectInput(invocation(map[string]string{
		"from": "dockerfile", "service-name": "app", "image": "busybox:1.36",
	}, nil), false)
	assertInputError(t, err, "Dockerfile 방식")

	_, err = collectInput(invocation(map[string]string{
		"from": "image", "service-name": "web", "image": "busybox:1.36", "context": "./ctx",
	}, nil), false)
	assertInputError(t, err, "이미지 방식")

	// A wizard apply can carry stale answers from steps that no longer apply;
	// the mode decides what is used, and stale irrelevant answers are ignored.
	input, err := collectInput(invocation(map[string]string{
		"from": "image", "service-name": "web", "image": "busybox:1.36",
		"context": "./ctx", "dockerfile": "Otherfile", "wizard": "apply",
	}, nil), true)
	if err != nil {
		t.Fatalf("wizard collect: %v", err)
	}
	if input.Context != "" || input.Dockerfile != "" {
		t.Fatalf("wizard kept irrelevant fields: %+v", input)
	}
}

func TestCollectSplitsWizardListsAndKeepsExplicitLists(t *testing.T) {
	input, err := collectInput(invocation(map[string]string{
		"from": "image", "service-name": "web", "image": "busybox:1.36",
		"port": "8080:80\n8443:443", "volume": "./data:/data:ro,Z\ncache:/cache",
		"env-var": "FOO=bar,baz\nBAZ=", "wizard": "apply",
	}, nil), true)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(input.Ports) != 2 || input.Ports[1] != "8443:443" {
		t.Fatalf("ports = %v", input.Ports)
	}
	if len(input.Volumes) != 2 || input.Volumes[0] != "./data:/data:ro,Z" {
		t.Fatalf("volumes = %v", input.Volumes)
	}
	if len(input.Env) != 2 || input.Env[0] != "FOO=bar,baz" || input.Env[1] != "BAZ=" {
		t.Fatalf("env = %v", input.Env)
	}

	input, err = collectInput(invocation(map[string]string{
		"from": "image", "service-name": "web", "image": "busybox:1.36",
		"port": "ignored, because lists win",
	}, map[string][]string{
		"port":   {"8080:80", "9090:90"},
		"volume": {"/abs/data:/data"},
	}), false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(input.Ports) != 2 || input.Ports[0] != "8080:80" {
		t.Fatalf("ports = %v", input.Ports)
	}
	if len(input.Volumes) != 1 || input.Volumes[0] != "/abs/data:/data" {
		t.Fatalf("volumes = %v", input.Volumes)
	}
}

func TestServiceNameValidation(t *testing.T) {
	valid := []string{"app", "web-1", "my_app", "my.app", "_app", "123"}
	for _, name := range valid {
		if err := validateServiceName(name); err != nil {
			t.Errorf("service %q rejected: %v", name, err)
		}
	}
	invalid := []string{"", "app name", "app/name", "app:name", "app@name", "웹"}
	for _, name := range invalid {
		if err := validateServiceName(name); err == nil {
			t.Errorf("service %q accepted", name)
		}
	}
}

func TestImageReferenceValidation(t *testing.T) {
	valid := []string{
		"busybox",
		"busybox:1.36",
		"nginx:1.27-alpine",
		"registry.example.com/team/app:1.2.3",
		"registry.example.com:5000/team/app",
		"localhost:5000/app:1.0",
		"owner/repo",
		"owner/repo:Tag_1",
		"a",
		"a.b/c-d_e",
		"nginx@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"nginx:tag@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for _, ref := range valid {
		if err := validateImageRef(ref); err != nil {
			t.Errorf("image %q rejected: %v", ref, err)
		}
	}
	invalid := []string{
		"", "BusyBox", "nginx:", ":tag", "nginx bad", "nginx:bad tag",
		"foo//bar", "Foo/bar:tag", "/nginx", "nginx/", "registry.example.com//app",
		"nginx@@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"nginx@sha256:abc", "nginx@notadigest:abc", "-nginx", ".nginx", "nginx..app",
		"nginx:tag:extra", "nginx@", "http://nginx",
		"nginx@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"nginx@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for _, ref := range invalid {
		if err := validateImageRef(ref); err == nil {
			t.Errorf("image %q accepted", ref)
		}
	}
}

func TestPortValidation(t *testing.T) {
	valid := []string{
		"80", "80/udp", "8080:80", "127.0.0.1:8080:80",
		"127.0.0.1::80", "[::1]:8080:80", "8000-8005:80-85",
	}
	for _, port := range valid {
		if err := validatePort(port); err != nil {
			t.Errorf("port %q rejected: %v", port, err)
		}
	}
	invalid := []string{
		"", "not-a-port", "8080:", ":80", "8080:99999", "0:80",
		"8080:80/udp2", "8080:80/tcp/udp", "127.0.0.1:8080",
		"8000-8005:80", "8000:80-85", "8005-8000:80-85",
		"127.0.0.1:0:80", "[::1:8080:80", "1.2.3.4.5:8080:80",
		"::80", "8080:80:90",
	}
	for _, port := range invalid {
		if err := validatePort(port); err == nil {
			t.Errorf("port %q accepted", port)
		}
	}
}

func TestVolumeValidation(t *testing.T) {
	valid := []string{
		"/data", "./data:/data", "../shared:/data:ro", "cache:/cache",
		"/abs/data:/data:ro,z", "~/.local/share/app:/data",
	}
	for _, volume := range valid {
		if _, err := parseVolume(volume); err != nil {
			t.Errorf("volume %q rejected: %v", volume, err)
		}
	}
	invalid := []string{
		"", ":/data", "data:", "relative/path:/data", "/data:/data:ro,bogus",
		"bad name:/data", "/data:relative", "/data:/data:ro:extra",
		"$HOME/data:/data", "./data:/../escape",
	}
	for _, volume := range invalid {
		if _, err := parseVolume(volume); err == nil {
			t.Errorf("volume %q accepted", volume)
		}
	}
}

func TestEnvironmentValidation(t *testing.T) {
	env, err := parseEnvironment([]string{"FOO=bar", "EMPTY=", "_KEY=value", "EQ=a=b"})
	if err != nil {
		t.Fatalf("parse env: %v", err)
	}
	if env["FOO"] != "bar" || env["EMPTY"] != "" || env["EQ"] != "a=b" {
		t.Fatalf("env = %#v", env)
	}
	invalid := [][]string{
		{"FOO"}, {"=bar"}, {"1KEY=value"}, {"BAD-KEY=value"},
		{"FOO=1", "FOO=2"},
	}
	for _, entries := range invalid {
		if _, err := parseEnvironment(entries); err == nil {
			t.Errorf("environment %v accepted", entries)
		}
	}
}

func TestRestartValidation(t *testing.T) {
	valid := []string{"", "no", "always", "unless-stopped", "on-failure", "on-failure:3"}
	for _, value := range valid {
		if err := validateRestart(value); err != nil {
			t.Errorf("restart %q rejected: %v", value, err)
		}
	}
	invalid := []string{"sometimes", "on-failure:", "on-failure:0", "on-failure:-1", "always:3"}
	for _, value := range invalid {
		if err := validateRestart(value); err == nil {
			t.Errorf("restart %q accepted", value)
		}
	}
}

func TestOutputPathValidationRejectsDirectoriesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	if _, err := destinationPath(dir, ""); err != nil {
		t.Fatalf("default destination: %v", err)
	}
	if _, err := destinationPath(dir, "-"); err == nil {
		t.Fatal("stdout destination accepted")
	}
	if _, err := destinationPath(dir, "~/compose.yaml"); err == nil {
		t.Fatal("tilde destination accepted")
	}
	if _, err := destinationPath(dir, "compose.yaml"); err != nil {
		t.Fatalf("relative destination: %v", err)
	}
}

// Every port rejection states the cause as a complete detail. The five branches
// below are the ones whose wording was broken by a copy rewrite: each once
// ended in a dangling particle before `)`, which reads as a truncated sentence
// instead of the next thing to fix.
func TestPortInvalidDetailsAreComplete(t *testing.T) {
	cases := []struct {
		port  string
		wants []string
	}{
		// An unterminated IPv6 bracket cannot be read at all.
		{"[::1", []string{"IPv6 주소는 [주소] 형식"}},
		// A valid bracket with no colon after it.
		{"[::1]8080:80", []string{"IPv6 주소 뒤에 :가 필요"}},
		// A bracket form with more than host and container after the IP.
		{"[::1]:8080:80:90", []string{"[IPv6]:호스트:컨테이너 형식"}},
		// One endpoint is a range and the other a single port.
		{"8000-8005:80", []string{"둘 다 범위 또는 둘 다 단일 값"}},
		// Both ranges, but of different lengths.
		{"8000-8005:80-90", []string{"범위 길이가 같아야 함"}},
		// The port is outside the only legal interval.
		{"70000:80", []string{"1-65535 범위"}},
	}
	for _, tc := range cases {
		err := validatePort(tc.port)
		ce, ok := err.(*core.Error)
		if !ok || len(ce.Diagnostics) == 0 {
			t.Fatalf("port %q: error = %T %v, want *core.Error", tc.port, err, err)
		}
		detail := ce.Diagnostics[0].Detail
		for _, want := range tc.wants {
			if !strings.Contains(detail, want) {
				t.Fatalf("port %q detail = %q, want it to state %q", tc.port, detail, want)
			}
		}
		// The broken shape: an ending was cut, leaving the detail without a
		// final word. Assert the closing detail is a whole fragment.
		if strings.Contains(detail, " )") || strings.HasSuffix(detail, "(") {
			t.Fatalf("port %q detail is a truncated fragment: %q", tc.port, detail)
		}
	}
}

// A comma inside a value is part of that value. The CLI no longer splits a
// repeatable answer on commas, so a command or environment value that contains
// one survives instead of silently becoming two values.
func TestRepeatableValuesKeepCommasAsData(t *testing.T) {
	input, err := collectInput(invocation(map[string]string{
		"from": "image", "service-name": "web", "image": "busybox:1.36",
		"command": "sh -c 'echo a,b'",
		"env-var": "PAIRS=a=1,b=2",
	}, nil), false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(input.Command) != 1 || input.Command[0] != "sh -c 'echo a,b'" {
		t.Fatalf("command = %v", input.Command)
	}
	if len(input.Env) != 1 || input.Env[0] != "PAIRS=a=1,b=2" {
		t.Fatalf("env = %v", input.Env)
	}
}

// A repeatable flag still supplies one value per occurrence.
func TestRepeatableFlagsKeepOccurrencesSeparate(t *testing.T) {
	input, err := collectInput(invocation(map[string]string{
		"from": "image", "service-name": "web", "image": "busybox:1.36",
	}, map[string][]string{
		"command": {"sh", "-c", "echo a,b"},
	}), false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(input.Command) != 3 || input.Command[2] != "echo a,b" {
		t.Fatalf("command = %v", input.Command)
	}
}
