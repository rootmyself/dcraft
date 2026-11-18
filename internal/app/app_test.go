package app_test

import (
	"io"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/app"
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
	names := map[string]bool{}
	for _, f := range features {
		names[f.Descriptor().Name] = true
	}
	for _, want := range []string{"status", "install-compose"} {
		if !names[want] {
			t.Fatalf("registered features = %v, want %q", names, want)
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
	for _, want := range []string{"status", "install-compose"} {
		if !commands[want] {
			t.Fatalf("%s command was not derived from the real registry: %v", want, commands)
		}
	}

	model := tui.NewModel(nil, tui.Options{Engine: a.Engine, Registry: a.Registry})
	viewText := model.View()
	if !strings.Contains(viewText, "스택 상태") {
		t.Fatalf("TUI did not open on the registered primary feature:\n%s", viewText)
	}
}
