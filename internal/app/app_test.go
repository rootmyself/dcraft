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
	if len(features) != 1 || features[0].Descriptor().Name != "status" {
		t.Fatalf("registered features = %v", features)
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
	found := false
	for _, cmd := range root.Commands() {
		if cmd.Name() == "status" {
			found = true
		}
	}
	if !found {
		t.Fatal("status command was not derived from the real registry")
	}

	model := tui.NewModel(nil, tui.Options{Engine: a.Engine, Registry: a.Registry})
	viewText := model.View()
	if !strings.Contains(viewText, "스택 상태") {
		t.Fatalf("TUI did not open on the registered primary feature:\n%s", viewText)
	}
}
