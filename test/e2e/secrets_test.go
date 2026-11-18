package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/secrets"
)

type secretsResult struct {
	SchemaVersion int               `json:"schema_version"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Target        core.Target       `json:"target"`
	Stages        []core.Stage      `json:"stages"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Secrets secrets.Report `json:"secrets"`
	} `json:"data"`
}

func parseSecrets(t *testing.T, out string) secretsResult {
	t.Helper()
	var res secretsResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse secrets JSON: %v\n%s", err, out)
	}
	return res
}

func readConfig(t *testing.T, project string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(project, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSecretsCLIAddListRemoveWithJSON(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	composePath := writeProject(t, project)
	state := t.TempDir()
	env := fake.env(t, state)

	res := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--delivery", "environment", "--source", "env", "--ref", "DB_PASSWORD", "--json")
	if res.code != 0 {
		t.Fatalf("add exit = %d stderr=%q", res.code, res.stderr)
	}
	if res.stderr != "" {
		t.Fatalf("JSON mode wrote stderr: %q", res.stderr)
	}
	added := parseSecrets(t, res.stdout)
	if added.Outcome != core.OutcomeSuccess || added.Action != "secrets" {
		t.Fatalf("result = %+v", added)
	}
	if added.Data.Secrets.Action != "add" || len(added.Data.Secrets.References) != 1 {
		t.Fatalf("add payload = %+v", added.Data.Secrets)
	}
	ref := added.Data.Secrets.References[0]
	if ref.Service != "web" || ref.Target != "DB_PASSWORD" || ref.Phase != "runtime" {
		t.Fatalf("reference = %+v", ref)
	}
	if !strings.Contains(ref.Effect, "환경변수") {
		t.Fatalf("effect = %q", ref.Effect)
	}
	for _, want := range []string{"resolve_target", "preflight", "validate", "save", "regenerate"} {
		if !hasStage(added.Stages, want) {
			t.Fatalf("stages = %+v, missing %q", added.Stages, want)
		}
	}
	config := readConfig(t, project)
	if !strings.Contains(config, "DB_PASSWORD") || strings.Contains(config, "value:") {
		t.Fatalf("config = %s", config)
	}
	if sha256File(t, composePath) == "" {
		t.Fatal("compose file disappeared")
	}

	list := runProcess(t, project, env, strings.NewReader(""), "secrets", "--json")
	if list.code != 0 {
		t.Fatalf("list exit = %d stderr=%q", list.code, list.stderr)
	}
	listed := parseSecrets(t, list.stdout)
	if listed.Data.Secrets.Action != "list" || len(listed.Data.Secrets.References) != 1 {
		t.Fatalf("list payload = %+v", listed.Data.Secrets)
	}
	if !hasStage(listed.Stages, "list") {
		t.Fatalf("list stages = %+v", listed.Stages)
	}

	removed := runProcess(t, project, env, strings.NewReader(""), "secrets", "--remove",
		"-s", "web", "--delivery", "environment", "--target", "DB_PASSWORD", "--json")
	if removed.code != 0 {
		t.Fatalf("remove exit = %d stderr=%q", removed.code, removed.stderr)
	}
	after := parseSecrets(t, removed.stdout)
	if after.Data.Secrets.Action != "remove" || len(after.Data.Secrets.References) != 0 {
		t.Fatalf("after remove = %+v", after.Data.Secrets)
	}
}

func TestSecretsCLIRemoveMissingInputExit2(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	res := runProcess(t, project, env, strings.NewReader(""), "secrets", "--remove", "-s", "web", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", res.code, res.stdout, res.stderr)
	}
	parsed := parseSecrets(t, res.stdout)
	if !hasCategory(secretsStatus(parsed), core.CategorySecretInputInvalid) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
}

func TestSecretsCLIReAddIsIdempotent(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	for i := 0; i < 2; i++ {
		res := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
			"-s", "web", "--delivery", "environment", "--source", "env", "--ref", "DB_PASSWORD", "--json")
		if res.code != 0 {
			t.Fatalf("add %d exit = %d stderr=%q", i, res.code, res.stderr)
		}
	}
	list := runProcess(t, project, env, strings.NewReader(""), "secrets", "--json")
	if list.code != 0 {
		t.Fatalf("list exit = %d", list.code)
	}
	if got := len(parseSecrets(t, list.stdout).Data.Secrets.References); got != 1 {
		t.Fatalf("references after re-add = %d, want 1", got)
	}
}

func TestSecretsCLIMissingInputExit2WithoutPrompt(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	res := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add", "-s", "web", "--json")
	if res.code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", res.code, res.stdout, res.stderr)
	}
	parsed := parseSecrets(t, res.stdout)
	if parsed.Outcome != core.OutcomeFailure {
		t.Fatalf("outcome = %q", parsed.Outcome)
	}
	if !hasCategory(secretsStatus(parsed), core.CategorySecretInputInvalid) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
}

func TestSecretsCLIRejectsUnknownServiceAndUnsupportedInputs(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	unknown := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "nope", "--delivery", "environment", "--source", "env", "--ref", "X", "--json")
	if unknown.code != 2 {
		t.Fatalf("unknown service exit = %d, want 2", unknown.code)
	}
	if !hasCategory(secretsStatus(parseSecrets(t, unknown.stdout)), core.CategoryServiceUnknown) {
		t.Fatalf("unknown service diagnostics = %+v", parseSecrets(t, unknown.stdout).Diagnostics)
	}

	build := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--phase", "build", "--delivery", "environment", "--source", "env", "--ref", "X", "--json")
	if build.code != 2 {
		t.Fatalf("build phase exit = %d, want 2", build.code)
	}
	if !hasCategory(secretsStatus(parseSecrets(t, build.stdout)), core.CategorySecretUnsupported) {
		t.Fatalf("build phase diagnostics = %+v", parseSecrets(t, build.stdout).Diagnostics)
	}

	aws := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--delivery", "environment", "--source", "aws", "--ref", "X", "--json")
	if aws.code != 2 {
		t.Fatalf("aws source exit = %d, want 2", aws.code)
	}
	if !hasCategory(secretsStatus(parseSecrets(t, aws.stdout)), core.CategorySecretUnsupported) {
		t.Fatalf("aws diagnostics = %+v", parseSecrets(t, aws.stdout).Diagnostics)
	}
}

func TestSecretsHumanOutputExplainsDelivery(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	res := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--delivery", "file", "--source", "file", "--ref", "secrets/token.txt")
	if res.code != 0 {
		t.Fatalf("exit = %d stderr=%q", res.code, res.stderr)
	}
	for _, want := range []string{"비밀값 참조를 저장했습니다", "/run/secrets/token", "직접 읽어야", "dcraft는 앱의 파일 소비를 보장하지 않습니다", "자동으로 찾아 제거하지 않습니다"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, res.stdout)
		}
	}
}

func TestUpAppliesRuntimeSecretOverrideWithoutChangingComposeFile(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	composePath := writeProject(t, project)
	before := sha256File(t, composePath)
	state := t.TempDir()
	sentinel := "sentinel-e2e-up-42fb"
	env := fake.envExtra(t, state, map[string]string{"DB_PASSWORD": sentinel})

	add := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--delivery", "environment", "--source", "env", "--ref", "DB_PASSWORD", "--json")
	if add.code != 0 {
		t.Fatalf("add exit = %d stderr=%q", add.code, add.stderr)
	}
	if strings.Contains(add.stdout, sentinel) {
		t.Fatal("add output leaked the value")
	}

	up := runProcess(t, project, env, strings.NewReader(""), "up", "--json")
	if up.code != 0 {
		t.Fatalf("up exit = %d stderr=%q\n%s", up.code, up.stderr, up.stdout)
	}
	if strings.Contains(up.stdout, sentinel) || strings.Contains(up.stderr, sentinel) {
		t.Fatal("up output leaked the value")
	}
	if sha256File(t, composePath) != before {
		t.Fatal("existing Compose file was modified")
	}
	calls := fake.calls(t)
	var upCall string
	for _, line := range calls {
		if strings.Contains(line, " up --detach") {
			upCall = line
		}
	}
	if upCall == "" {
		t.Fatalf("up was not called: %v", calls)
	}
	if !strings.Contains(upCall, "compose.secrets.yaml") {
		t.Fatalf("up did not use the managed override: %s", upCall)
	}
	// Scan the state directory and the result for the value.
	if err := filepath.Walk(state, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(data), sentinel) {
			t.Fatalf("state file %s contains the sentinel value", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUpFailsBeforeChangingServicesWhenSourceMissing(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	composePath := writeProject(t, project)
	before := sha256File(t, composePath)
	state := t.TempDir()
	env := fake.env(t, state)

	add := runProcess(t, project, env, strings.NewReader(""), "secrets", "--add",
		"-s", "web", "--delivery", "environment", "--source", "env", "--ref", "MISSING_SECRET", "--json")
	if add.code != 0 {
		t.Fatalf("add exit = %d", add.code)
	}
	up := runProcess(t, project, env, strings.NewReader(""), "up", "--json")
	if up.code != 1 {
		t.Fatalf("up exit = %d, want 1\n%s", up.code, up.stdout)
	}
	parsed := parseStatus(t, up.stdout)
	if !hasCategory(parsed, core.CategorySecretSource) {
		t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
	}
	for _, line := range fake.calls(t) {
		if strings.Contains(line, " up --detach") {
			t.Fatalf("up ran despite the missing source: %s", line)
		}
	}
	if sha256File(t, composePath) != before {
		t.Fatal("Compose file changed on failure")
	}
}

func hasStage(stages []core.Stage, name string) bool {
	for _, stage := range stages {
		if stage.Name == name && stage.Status == core.StageSucceeded {
			return true
		}
	}
	return false
}

func secretsStatus(res secretsResult) statusJSON {
	return statusJSON{
		SchemaVersion: res.SchemaVersion,
		Action:        res.Action,
		Outcome:       res.Outcome,
		Target:        res.Target,
		Stages:        res.Stages,
		Diagnostics:   res.Diagnostics,
	}
}
