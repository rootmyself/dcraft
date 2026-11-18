package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// updateFakeDocker is a scriptable docker boundary for the explicit update
// feature. It records every call and serves config/container/image state from
// files so the CLI process can be exercised without Docker.
type updateFakeDocker struct {
	dir     string
	log     string
	config  string
	psq     string
	ps      string
	inspect string
	images  string
}

const updateConfigJSON = `{"name":"fake-project","services":{
 "web":{"image":"nginx:1.27","healthcheck":{"test":["CMD","true"]}},
 "worker":{"image":"worker:local"},
 "builder":{"build":{"context":"."}}
}}`

const updateInspectJSON = `[
 {"Id":"aaa","Image":"sha256:1111","Created":"2026-11-18T10:00:00Z","RestartCount":0,
  "State":{"Status":"running","Running":true,"Restarting":false,"ExitCode":0,"Health":{"Status":"healthy"}},
  "Config":{"Image":"nginx:1.27","Labels":{"com.docker.compose.service":"web"}}},
 {"Id":"bbb","Image":"sha256:2222","Created":"2026-11-18T10:00:01Z","RestartCount":0,
  "State":{"Status":"running","Running":true,"Restarting":false,"ExitCode":0},
  "Config":{"Image":"worker:local","Labels":{"com.docker.compose.service":"worker"}}},
 {"Id":"ccc","Image":"sha256:3333","Created":"2026-11-18T10:00:02Z","RestartCount":0,
  "State":{"Status":"running","Running":true,"Restarting":false,"ExitCode":0},
  "Config":{"Image":"fake-project-builder","Labels":{"com.docker.compose.service":"builder"}}}
]`

const updateImages = `nginx:1.27 sha256:1111
worker:local sha256:2222
`

func newUpdateFakeDocker(t *testing.T) *updateFakeDocker {
	t.Helper()
	dir := t.TempDir()
	f := &updateFakeDocker{
		dir:     dir,
		log:     filepath.Join(dir, "calls.log"),
		config:  filepath.Join(dir, "config.json"),
		psq:     filepath.Join(dir, "psq.txt"),
		ps:      filepath.Join(dir, "ps.ndjson"),
		inspect: filepath.Join(dir, "inspect.json"),
		images:  filepath.Join(dir, "images.txt"),
	}
	writeFile := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(f.config, updateConfigJSON)
	writeFile(f.psq, "aaa bbb ccc\n")
	writeFile(f.inspect, updateInspectJSON)
	writeFile(f.images, updateImages)
	ps := strings.Join([]string{
		`{"Name":"fake-project-web-1","Service":"web","State":"running","Health":"healthy","Image":"nginx:1.27","ExitCode":0}`,
		`{"Name":"fake-project-worker-1","Service":"worker","State":"running","Health":"","Image":"worker:local","ExitCode":0}`,
	}, "\n")
	writeFile(f.ps, ps)

	script := `#!/bin/sh
LOG="${DOCKER_FAKE_LOG:?}"
printf '%s\n' "$*" >> "$LOG"
case "$*" in
  "--version") echo "Docker version 29.10.0, build fake"; exit 0 ;;
  "compose version --short") echo "5.7.0"; exit 0 ;;
  "context ls --format json") printf '%s\n' '{"Current":true,"Description":"Docker Desktop","DockerEndpoint":"unix:///Users/me/.docker/run/docker.sock","Error":"","Name":"desktop-linux"}'; exit 0 ;;
  "info --format {{.ServerVersion}}") echo "29.9.3"; exit 0 ;;
esac
case "$*" in
  *dcraft-capability-probe*config*)
    echo '{"name":"dcraft-capability-probe","services":{"probe":{"image":"scratch"}}}'; exit 0 ;;
  *dcraft-capability-probe*ps*)
    exit 0 ;;
  *" -f - --project-directory "*)
    dir=""; prev=""
    for a in "$@"; do
      if [ "$prev" = "--project-directory" ]; then dir="$a"; fi
      prev="$a"
    done
    name=$(basename "$dir" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-z0-9_-')
    echo "{\"name\":\"$name\"}"
    exit 0 ;;
  "compose ls -a --format json") echo "[]"; exit 0 ;;
  *" config --format json") cat "$DOCKER_FAKE_CONFIG"; exit 0 ;;
  *" ps -a -q") cat "$DOCKER_FAKE_PSQ"; exit 0 ;;
  *" ps --all --format json") cat "$DOCKER_FAKE_PS"; exit 0 ;;
esac
if [ "$1" = "inspect" ]; then cat "$DOCKER_FAKE_INSPECT"; exit 0; fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then
  ref="$3"
  id=$(awk -v r="$ref" '$1==r {print $2}' "$DOCKER_FAKE_IMAGES")
  if [ -z "$id" ]; then echo "Error response from daemon: No such image: $ref" >&2; exit 1; fi
  echo "$id"
  exit 0
fi
if [ "$1" = "tag" ]; then
  printf '%s %s\n' "$3" "$2" >> "$DOCKER_FAKE_IMAGES"
  exit 0
fi
sub=""
for a in "$@"; do
  case "$a" in
    build|pull|up|down|stop|restart) sub="$a"; break ;;
  esac
done
case "$sub" in
  build)
    if [ -n "$DOCKER_FAKE_SLEEP_BUILD" ]; then exec sleep "$DOCKER_FAKE_SLEEP_BUILD"; fi
    if [ "$DOCKER_FAKE_FAIL_STAGE" = "build" ]; then echo "build failed for testing" >&2; exit 1; fi
    echo "built"; exit 0 ;;
  pull)
    if [ "$DOCKER_FAKE_FAIL_STAGE" = "pull" ]; then echo "pull access denied for worker, repository does not exist" >&2; exit 1; fi
    echo "pulled"; exit 0 ;;
  up)
    if [ -n "$DOCKER_FAKE_SLEEP_UP" ]; then exec sleep "$DOCKER_FAKE_SLEEP_UP"; fi
    if [ "$DOCKER_FAKE_FAIL_STAGE" = "up" ]; then echo "up failed for testing" >&2; exit 1; fi
    echo "started"; exit 0 ;;
  down|stop|restart) echo "unexpected lifecycle call" >&2; exit 8 ;;
esac
echo "fake docker: unexpected invocation: $*" >&2
exit 9
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *updateFakeDocker) env(t *testing.T, stateDir string, extra map[string]string) []string {
	t.Helper()
	values := map[string]string{
		"PATH":                   f.dir + ":" + os.Getenv("PATH"),
		"DOCKER_FAKE_LOG":        f.log,
		"DOCKER_FAKE_CONFIG":     f.config,
		"DOCKER_FAKE_PSQ":        f.psq,
		"DOCKER_FAKE_PS":         f.ps,
		"DOCKER_FAKE_INSPECT":    f.inspect,
		"DOCKER_FAKE_IMAGES":     f.images,
		"DOCKER_FAKE_FAIL_STAGE": "",
		"DCRAFT_STATE_DIR":       stateDir,
	}
	for k, v := range extra {
		values[k] = v
	}
	return buildEnv(values)
}

func (f *updateFakeDocker) write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *updateFakeDocker) calls(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.log)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

type updateJSON struct {
	SchemaVersion int               `json:"schema_version"`
	OperationID   string            `json:"operation_id"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Target        core.Target       `json:"target"`
	Stages        []core.Stage      `json:"stages"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Update struct {
			ProjectName  string                   `json:"project_name"`
			Scope        string                   `json:"scope"`
			Services     []string                 `json:"services"`
			Options      map[string]any           `json:"options"`
			Plan         map[string]any           `json:"plan"`
			Built        []string                 `json:"built"`
			Pulled       []string                 `json:"pulled"`
			Applied      bool                     `json:"applied"`
			Verified     bool                     `json:"verified"`
			Readiness    []stack.ServiceReadiness `json:"readiness"`
			RecordStatus string                   `json:"record_status"`
			GoodVersion  *struct {
				Target   map[string]any `json:"target"`
				Services []struct {
					Service      string `json:"service"`
					ImageID      string `json:"image_id"`
					RetentionTag string `json:"retention_tag"`
				} `json:"services"`
			} `json:"good_version"`
			Notices []string `json:"notices"`
		} `json:"update"`
	} `json:"data"`
}

func parseUpdate(t *testing.T, out string) updateJSON {
	t.Helper()
	var res updateJSON
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse update JSON: %v\n%s", err, out)
	}
	return res
}

func updateCategories(res updateJSON) []core.Category {
	var out []core.Category
	for _, d := range res.Diagnostics {
		out = append(out, d.Category)
	}
	return out
}

func hasUpdateCategory(res updateJSON, cat string) bool {
	for _, d := range res.Diagnostics {
		if string(d.Category) == cat {
			return true
		}
	}
	return false
}

func TestUpdateCLIReportsPlanFlagsAndReadiness(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()

	res := runProcess(t, project, fake.env(t, stateDir, nil), strings.NewReader(""),
		"update", "--path", project, "--no-cache", "--build-pull", "--force-recreate")
	if res.code != 0 {
		t.Fatalf("update exit=%d stderr=%q", res.code, res.stderr)
	}
	for _, want := range []string{
		"갱신 대상", "예정 작업", "캐시 사용 안 함", "강제 재생성",
		"정확한 미래 상태 diff가 아닙니다", "healthcheck 통과", "앱 확인 안 됨",
		"정상 버전 기록", "dcraft-good/",
	} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("human output misses %q:\n%s", want, res.stdout)
		}
	}

	calls := fake.calls(t)
	build := findCall(t, calls, " build ")
	if !strings.Contains(build, "--no-cache") || !strings.Contains(build, "--pull") || !strings.Contains(build, "builder") {
		t.Fatalf("build call = %q", build)
	}
	pull := findCall(t, calls, " pull ")
	if strings.Contains(pull, "builder") {
		t.Fatalf("pull included a local build service: %q", pull)
	}
	if !strings.Contains(pull, "web") || !strings.Contains(pull, "worker") {
		t.Fatalf("pull call = %q", pull)
	}
	up := findCall(t, calls, " up ")
	if !strings.Contains(up, "--detach") || !strings.Contains(up, "--force-recreate") || !strings.Contains(up, "--pull missing") {
		t.Fatalf("apply call = %q", up)
	}
	for _, line := range calls {
		if strings.Contains(line, "--wait") {
			t.Fatalf("update used the untrusted --wait: %q", line)
		}
		if strings.Contains(line, " down") || strings.Contains(line, " prune") || strings.Contains(line, "image rm") {
			t.Fatalf("update removed resources: %q", line)
		}
	}
}

func TestUpdateJSONSeparatesOutcomeAndDoesNotLeakSecrets(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()
	const secret = "s3cr3t-update-value"
	res := runProcess(t, project, fake.env(t, stateDir, map[string]string{"APP_TOKEN": secret}), strings.NewReader(""),
		"update", "--path", project, "--json")
	if res.code != 0 || res.stderr != "" {
		t.Fatalf("update exit=%d stderr=%q", res.code, res.stderr)
	}
	parsed := parseUpdate(t, res.stdout)
	if parsed.Outcome != core.OutcomeSuccess || parsed.Action != "update" {
		t.Fatalf("result = %+v", parsed)
	}
	if parsed.Target.ProjectDir != project || parsed.Target.Files == nil {
		t.Fatalf("target = %+v", parsed.Target)
	}
	var stageNames []string
	for _, st := range parsed.Stages {
		stageNames = append(stageNames, st.Name)
	}
	want := "resolve_target,preflight,resolve_project,allocate_name,coordinate,plan,build,pull,apply,verify,record,history"
	if got := strings.Join(stageNames, ","); got != want {
		t.Fatalf("stages = %q, want %q", got, want)
	}
	report := parsed.Data.Update
	if !report.Applied || report.RecordStatus != "recorded" || report.GoodVersion == nil {
		t.Fatalf("report = %+v", report)
	}
	if strings.Join(report.Built, ",") != "builder" {
		t.Fatalf("built = %v", report.Built)
	}
	if strings.Join(report.Pulled, ",") != "web,worker" {
		t.Fatalf("pulled = %v", report.Pulled)
	}
	statuses := map[string]stack.Readiness{}
	for _, svc := range report.Readiness {
		statuses[svc.Service] = svc.Status
	}
	if statuses["web"] != stack.ReadinessHealthy || statuses["worker"] != stack.ReadinessRunningUnverified {
		t.Fatalf("readiness = %+v", report.Readiness)
	}
	if report.Verified {
		t.Fatal("an update containing a service without a healthcheck claimed full verification")
	}
	if len(report.GoodVersion.Services) != 3 {
		t.Fatalf("recorded services = %+v", report.GoodVersion.Services)
	}
	for _, svc := range report.GoodVersion.Services {
		if !strings.HasPrefix(svc.ImageID, "sha256:") || !strings.HasPrefix(svc.RetentionTag, "dcraft-good/") {
			t.Fatalf("recorded identity = %+v", svc)
		}
	}
	if strings.Contains(res.stdout, secret) || strings.Contains(historyFor(t, stateDir), secret) {
		t.Fatal("secret value leaked into output or history")
	}
	goodPath := filepath.Join(stateDir, "stacks")
	data, err := os.ReadFile(filepath.Join(findStateFile(t, goodPath, "state.json")))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("secret value leaked into the good-version record")
	}
}

func findStateFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && info.Name() == name {
			found = path
		}
		return nil
	})
	if err != nil || found == "" {
		t.Fatalf("state file %q not found under %s: %v", name, root, err)
	}
	return found
}

func TestUpdateInvalidTimeoutExits2(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""),
		"update", "--path", project, "--timeout", "0", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr=%q)", res.code, res.stderr)
	}
	parsed := parseUpdate(t, res.stdout)
	if !hasUpdateCategory(parsed, "invalid_option") {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " build ") || strings.Contains(line, " pull ") || strings.Contains(line, " up ") {
			t.Fatalf("invalid input reached a mutating Docker call: %q", line)
		}
	}
}

func TestUpdateNoPullMissingImageFailsBeforeChanging(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	fake.write(t, fake.config, `{"name":"fake-project","services":{"web":{"image":"localonly:1"}}}`)
	fake.csq(t, "aaa")
	fake.write(t, fake.inspect, `[{"Id":"aaa","Image":"sha256:1111","RestartCount":0,
	 "State":{"Status":"running","Running":true},
	 "Config":{"Image":"localonly:1","Labels":{"com.docker.compose.service":"web"}}}]`)

	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""),
		"update", "--path", project, "--no-pull", "--json")
	if res.code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr=%q)", res.code, res.stderr)
	}
	parsed := parseUpdate(t, res.stdout)
	if !hasUpdateCategory(parsed, "image_missing") {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " up ") || strings.Contains(line, " pull ") || strings.Contains(line, " build ") {
			t.Fatalf("no-pull changed the stack before the image check: %q", line)
		}
	}
}

func (f *updateFakeDocker) csq(t *testing.T, content string) {
	t.Helper()
	f.write(t, f.psq, content+"\n")
}

func TestUpdateFailedReadinessKeepsPreviousGoodRecord(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()
	env := fake.env(t, stateDir, nil)

	first := runProcess(t, project, env, strings.NewReader(""), "update", "--path", project, "--json")
	if first.code != 0 {
		t.Fatalf("first update exit=%d stderr=%q", first.code, first.stderr)
	}
	firstParsed := parseUpdate(t, first.stdout)
	recorded := firstParsed.Data.Update.GoodVersion.Services[0]
	statePath := findStateFile(t, filepath.Join(stateDir, "stacks"), "state.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}

	fake.write(t, fake.inspect, `[{"Id":"aaa","Image":"sha256:9999","RestartCount":2,
	 "State":{"Status":"running","Running":true,"Health":{"Status":"unhealthy"}},
	 "Config":{"Image":"nginx:1.27","Labels":{"com.docker.compose.service":"web"}}}]`)
	fake.csq(t, "aaa")
	second := runProcess(t, project, env, strings.NewReader(""), "update", "--path", project, "--json")
	if second.code != 1 {
		t.Fatalf("unhealthy update exit = %d, want 1", second.code)
	}
	secondParsed := parseUpdate(t, second.stdout)
	if !hasUpdateCategory(secondParsed, "readiness_failed") {
		t.Fatalf("diagnostics = %+v", secondParsed.Diagnostics)
	}
	verifyFailed := false
	for _, st := range secondParsed.Stages {
		if st.Name == "verify" && st.Status == core.StageFailed {
			verifyFailed = true
		}
	}
	if !verifyFailed {
		t.Fatalf("verify stage was not marked failed: %+v", secondParsed.Stages)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed update replaced the good record:\nbefore=%s\nafter=%s", before, after)
	}
	if !strings.Contains(string(after), recorded.ImageID) {
		t.Fatalf("old image ID was lost: %s", after)
	}
	tags := 0
	for _, line := range fake.calls(t) {
		if strings.HasPrefix(line, "tag ") {
			tags++
		}
	}
	if tags != len(firstParsed.Data.Update.GoodVersion.Services) {
		t.Fatalf("failed update created extra tags: %d", tags)
	}
}

func TestUpdateCancellationExits130(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	stateDir := t.TempDir()
	env := fake.env(t, stateDir, map[string]string{"DOCKER_FAKE_SLEEP_UP": "10"})
	cmd := exec.Command(binaryPath, "update", "--path", project, "--json")
	cmd.Dir = project
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second)
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("wait: %v", err)
		}
		code = exitErr.ExitCode()
	}
	if code != 130 {
		t.Fatalf("exit = %d, want 130 (stderr=%q)", code, stderr.String())
	}
	parsed := parseUpdate(t, stdout.String())
	if parsed.Outcome != core.OutcomeCanceled || !hasUpdateCategory(parsed, "canceled") {
		t.Fatalf("result = %+v", parsed)
	}
	if path := findStateFileIfAny(filepath.Join(stateDir, "stacks"), "state.json"); path != "" {
		if data, _ := os.ReadFile(path); len(data) > 0 {
			t.Fatal("canceled update recorded a good version")
		}
	}
}

func findStateFileIfAny(root, name string) string {
	var found string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && info.Name() == name {
			found = path
		}
		return nil
	})
	return found
}

func TestUpdateServiceSelectionOnlyTouchesSelection(t *testing.T) {
	fake := newUpdateFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	res := runProcess(t, project, fake.env(t, t.TempDir(), nil), strings.NewReader(""),
		"update", "--path", project, "-s", "worker", "--json")
	if res.code != 0 {
		t.Fatalf("update exit=%d stderr=%q", res.code, res.stderr)
	}
	parsed := parseUpdate(t, res.stdout)
	report := parsed.Data.Update
	if report.Scope != "services" || strings.Join(report.Services, ",") != "worker" {
		t.Fatalf("scope = %+v", report)
	}
	if len(report.Readiness) != 1 || report.Readiness[0].Service != "worker" {
		t.Fatalf("readiness = %+v", report.Readiness)
	}
	if len(report.Built) != 0 {
		t.Fatalf("selected image-only update built something: %v", report.Built)
	}
	if strings.Join(report.Pulled, ",") != "worker" {
		t.Fatalf("pulled = %v", report.Pulled)
	}
	up := findCall(t, fake.calls(t), " up ")
	if !strings.HasSuffix(up, " worker") {
		t.Fatalf("apply call = %q", up)
	}
}
