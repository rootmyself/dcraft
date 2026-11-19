package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type environmentResult struct {
	SchemaVersion int               `json:"schema_version"`
	Action        string            `json:"action"`
	Outcome       core.Outcome      `json:"outcome"`
	Target        core.Target       `json:"target"`
	Diagnostics   []core.Diagnostic `json:"diagnostics"`
	Data          struct {
		Environments struct {
			Action     string `json:"action"`
			ConfigFile string `json:"config_file"`
			Aliases    []struct {
				Name     string   `json:"name"`
				Files    []string `json:"files"`
				EnvFiles []string `json:"env_files"`
				Profiles []string `json:"profiles"`
			} `json:"aliases"`
		} `json:"environments"`
	} `json:"data"`
}

func parseEnvironmentResult(t *testing.T, out string) environmentResult {
	t.Helper()
	var res environmentResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parse environments JSON: %v\n%s", err, out)
	}
	return res
}

func TestEnvironmentCLIAddListRemoveAndStatusSelection(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	compose := writeProject(t, project)
	devCompose := filepath.Join(project, "compose.dev.yaml")
	if err := os.WriteFile(devCompose, []byte("services:\n  debug:\n    image: nginx:alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(project, ".env.dev")
	if err := os.WriteFile(envFile, []byte("TOP_SECRET=do-not-print-me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{
		compose:    sha256File(t, compose),
		devCompose: sha256File(t, devCompose),
	}
	env := fake.env(t, t.TempDir())

	add := runProcess(t, project, env, strings.NewReader(""),
		"environments", "--add", "--name", "dev",
		"--file", "compose.yaml", "--file", "compose.dev.yaml",
		"--env-file", ".env.dev", "--profile", "dev", "--json")
	if add.code != 0 {
		t.Fatalf("add exit = %d stderr=%q", add.code, add.stderr)
	}
	if add.stderr != "" {
		t.Fatalf("JSON add wrote stderr: %q", add.stderr)
	}
	for _, forbidden := range []string{"TOP_SECRET", "do-not-print-me"} {
		if strings.Contains(add.stdout+add.stderr, forbidden) {
			t.Fatalf("add output leaked env-file content %q:\n%s", forbidden, add.stdout)
		}
	}
	added := parseEnvironmentResult(t, add.stdout)
	if added.Outcome != core.OutcomeSuccess || added.Data.Environments.Action != "add" {
		t.Fatalf("add result = %+v", added)
	}
	if len(added.Data.Environments.Aliases) != 1 {
		t.Fatalf("aliases = %+v", added.Data.Environments.Aliases)
	}
	alias := added.Data.Environments.Aliases[0]
	if alias.Name != "dev" || len(alias.Files) != 2 || len(alias.EnvFiles) != 1 || strings.Join(alias.Profiles, ",") != "dev" {
		t.Fatalf("alias = %+v", alias)
	}
	if !samePath(t, alias.Files[0], compose) || !samePath(t, alias.Files[1], devCompose) || !samePath(t, alias.EnvFiles[0], envFile) {
		t.Fatalf("alias paths = %+v", alias)
	}
	assertFilesUnchanged(t, hashes)

	list := runProcess(t, project, env, strings.NewReader(""), "environments", "--list", "--json")
	if list.code != 0 {
		t.Fatalf("list exit = %d stderr=%q", list.code, list.stderr)
	}
	listed := parseEnvironmentResult(t, list.stdout)
	if listed.Data.Environments.Action != "list" || len(listed.Data.Environments.Aliases) != 1 || listed.Data.Environments.Aliases[0].Name != "dev" {
		t.Fatalf("list result = %+v", listed.Data.Environments)
	}

	status := runProcess(t, project, env, strings.NewReader(""), "status", "--env", "dev", "--json")
	if status.code != 0 {
		t.Fatalf("status exit = %d stderr=%q", status.code, status.stderr)
	}
	parsed := parseStatus(t, status.stdout)
	if parsed.Target.Environment != "dev" {
		t.Fatalf("target.environment = %q", parsed.Target.Environment)
	}
	if len(parsed.Target.Files) != 2 || !samePath(t, parsed.Target.Files[1], devCompose) {
		t.Fatalf("target.files = %v", parsed.Target.Files)
	}
	if len(parsed.Target.EnvFiles) != 1 || !samePath(t, parsed.Target.EnvFiles[0], envFile) || strings.Join(parsed.Target.Profiles, ",") != "dev" {
		t.Fatalf("target env/profile = %v / %v", parsed.Target.EnvFiles, parsed.Target.Profiles)
	}

	remove := runProcess(t, project, env, strings.NewReader(""), "environments", "--remove", "--name", "dev", "--json")
	if remove.code != 0 {
		t.Fatalf("remove exit = %d stderr=%q", remove.code, remove.stderr)
	}
	removed := parseEnvironmentResult(t, remove.stdout)
	if removed.Data.Environments.Action != "remove" || len(removed.Data.Environments.Aliases) != 0 {
		t.Fatalf("remove result = %+v", removed.Data.Environments)
	}
	missing := runProcess(t, project, env, strings.NewReader(""), "status", "--env", "dev", "--json")
	if missing.code != 2 {
		t.Fatalf("status after remove exit = %d, want 2", missing.code)
	}
	assertFilesUnchanged(t, hashes)
}

func TestEnvironmentCLIInvalidInputExitsTwoWithoutPrompting(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())
	cases := []struct {
		name string
		args []string
	}{
		{"no action", []string{"environments", "--json"}},
		{"no name", []string{"environments", "--add", "--json"}},
		{"invalid name", []string{"environments", "--add", "--name", "Dev", "--profile", "dev", "--json"}},
		{"two actions", []string{"environments", "--add", "--list", "--name", "dev", "--json"}},
		{"alias and compose file", []string{"environments", "--add", "--name", "dev", "--file", "compose.yaml", "--env", "other", "--json"}},
		{"unknown remove", []string{"environments", "--remove", "--name", "missing", "--json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runProcess(t, project, env, strings.NewReader(""), tc.args...)
			if res.code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr=%q stdout=%q)", res.code, res.stderr, res.stdout)
			}
			if strings.Contains(res.stdout, "\x1b[") || strings.Contains(res.stderr, "?") {
				t.Fatalf("non-interactive run prompted or opened a TUI: stdout=%q stderr=%q", res.stdout, res.stderr)
			}
			parsed := parseEnvironmentResult(t, res.stdout)
			if parsed.Outcome != core.OutcomeFailure || len(parsed.Diagnostics) == 0 {
				t.Fatalf("diagnostics = %+v", parsed.Diagnostics)
			}
			for _, d := range parsed.Diagnostics {
				if len(d.NextActions) == 0 && d.Severity == core.SeverityError {
					t.Fatalf("diagnostic without Korean next action: %+v", d)
				}
			}
		})
	}
}

func TestEnvironmentTUIWizardPreviewDeclineThenAccept(t *testing.T) {
	fake := newFakeDocker(t)
	project := t.TempDir()
	compose := writeProject(t, project)
	envFile := filepath.Join(project, ".env.dev")
	if err := os.WriteFile(envFile, []byte("TOP_SECRET=hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{compose: sha256File(t, compose)}
	env := fake.env(t, t.TempDir())
	down := strings.Repeat("\x1b[B", menuDownPresses(t, "environments"))

	// Decline at the preview: esc must leave the folder untouched.
	output, code := runPTYSteps(t, project, env, []ptyStep{
		{waitText: "스택 상태 (status)"},
		{waitText: "↑/↓ 선택"},
		{send: down},
		{waitText: "실행환경 별칭 (environments)", send: "\r"},
		{waitText: "별칭 이름", send: "dev\r"},
		{waitText: "Compose 파일", send: "\r"},
		{waitText: "환경 파일", send: ".env.dev\r"},
		{waitText: "Compose 프로필", send: "dev\r"},
		{waitText: "다음 실행환경 별칭을 저장할까요?"},
		{waitText: "environments:"},
		{send: "\x1b"},
		{waitText: "저장된 내용은 없습니다"},
		{send: "q"},
	})
	if code != 0 {
		t.Fatalf("decline TUI exit = %d\n%s", code, output)
	}
	for _, want := range []string{"다음 실행환경 별칭을 저장할까요?", "compose.yaml", ".env.dev", "저장된 내용은 없습니다"} {
		if !strings.Contains(output, want) {
			t.Fatalf("decline TUI output missing %q:\n%s", want, output)
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".dcraft.yaml")); !os.IsNotExist(err) {
		t.Fatalf("declined wizard wrote .dcraft.yaml: %v", err)
	}
	if strings.Contains(output, "TOP_SECRET") || strings.Contains(output, "hidden") {
		t.Fatalf("TUI preview leaked env-file content:\n%s", output)
	}
	assertFilesUnchanged(t, hashes)

	// Accept the same flow: enter saves, then the presenter confirms.
	output, code = runPTYSteps(t, project, env, []ptyStep{
		{waitText: "스택 상태 (status)"},
		{waitText: "↑/↓ 선택"},
		{send: down},
		{waitText: "실행환경 별칭 (environments)", send: "\r"},
		{waitText: "별칭 이름", send: "dev\r"},
		{waitText: "Compose 파일", send: "\r"},
		{waitText: "환경 파일", send: ".env.dev\r"},
		{waitText: "Compose 프로필", send: "dev\r"},
		{waitText: "다음 실행환경 별칭을 저장할까요?", send: "\r"},
		{waitText: "별칭을 저장했습니다"},
		{send: "q"},
	})
	if code != 0 {
		t.Fatalf("accept TUI exit = %d\n%s", code, output)
	}
	config, err := os.ReadFile(filepath.Join(project, ".dcraft.yaml"))
	if err != nil {
		t.Fatalf("accepted wizard did not write config: %v", err)
	}
	for _, want := range []string{"environments:", "dev:", "compose.yaml", ".env.dev", "dev"} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	assertFilesUnchanged(t, hashes)
}
