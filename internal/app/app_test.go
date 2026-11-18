package app_test

import (
	"io"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/app"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/ui/cli"
	"github.com/rootmyself/dcraft/internal/ui/tui"
)

func testEnv(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestRegistrationWiresStatusIntoBothInterfaces(t *testing.T) {
	stateDir := t.TempDir()
	a, err := app.New(app.Options{
		Getenv: testEnv(map[string]string{"DCRAFT_STATE_DIR": stateDir}),
		Getwd:  func() (string, error) { return t.TempDir(), nil },
		Runner: &execxtest.Runner{},
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	if a.Registry == nil || a.Engine == nil || a.Deps == nil {
		t.Fatalf("incomplete app graph: %+v", a)
	}
	features := a.Registry.All()
	byName := map[string]core.Feature{}
	for _, f := range features {
		byName[f.Descriptor().Name] = f
	}
	for _, name := range []string{"status", "install-compose", "up", "stop", "down", "restart", "update", "secrets", "hooks", "logs", "exec", "backup", "restore", "rollback", "init"} {
		if _, ok := byName[name]; !ok {
			t.Fatalf("registered features = %v, missing %q", features, name)
		}
	}
	if len(features) != 15 {
		t.Fatalf("registered features = %d, want 15", len(features))
	}
	for _, name := range []string{"up", "stop", "restart", "update"} {
		if !byName[name].Descriptor().SupportsServices {
			t.Fatalf("%s does not accept service selection", name)
		}
	}
	if byName["down"].Descriptor().SupportsServices {
		t.Fatal("down must stay project-wide")
	}
	for _, name := range []string{"up", "stop", "down", "restart", "update", "rollback"} {
		d := byName[name].Descriptor()
		if d.ReadOnly || !d.RecordsHistory {
			t.Fatalf("%s descriptor = %+v", name, d)
		}
	}
	primary, ok := a.Registry.Primary()
	if !ok || primary.Descriptor().Name != "status" {
		t.Fatalf("primary = %v, %v", primary, ok)
	}

	root := cli.New(cli.Options{
		Engine:   a.Engine,
		Registry: a.Registry,
		Stdout:   io.Discard,
		Stderr:   io.Discard,
	})
	commands := map[string]bool{}
	for _, cmd := range root.Commands() {
		commands[cmd.Name()] = true
	}
	for _, name := range []string{"status", "install-compose", "up", "stop", "down", "restart", "update", "secrets", "hooks", "logs", "exec", "backup", "restore", "rollback", "init"} {
		if !commands[name] {
			t.Fatalf("command %q was not derived from the real registry", name)
		}
	}

	model := tui.NewModel(nil, tui.Options{Engine: a.Engine, Registry: a.Registry})
	viewText := model.View()
	if !strings.Contains(viewText, "스택 상태") {
		t.Fatalf("TUI did not open on the registered primary feature:\n%s", viewText)
	}
}
