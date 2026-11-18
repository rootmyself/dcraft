package compose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/stack"
)

func lifecycleTarget() core.Target {
	return core.Target{
		ProjectDir:  "/p",
		Files:       []string{"/p/compose.yaml", "/p/compose.override.yaml"},
		EnvFiles:    []string{"/p/custom.env"},
		Profiles:    []string{"dev"},
		ProjectName: "hello-world",
	}
}

func runLifecycle(t *testing.T, runner *execxtest.Runner, fn func(*Compose) (stack.OpResult, error)) (stack.OpResult, error, []string) {
	t.Helper()
	res, err := fn(New(runner, nil))
	return res, err, runner.CallLines()
}

func TestUpUsesDetachAfterGlobalFlagsAndKeepsServiceOrder(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte("ok")}, nil
	}}
	_, err, calls := runLifecycle(t, runner, func(c *Compose) (stack.OpResult, error) {
		return c.Up(context.Background(), lifecycleTarget(), []string{"web", "api"})
	})
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	want := "docker compose -f /p/compose.yaml -f /p/compose.override.yaml --env-file /p/custom.env --profile dev -p hello-world up --detach web api"
	if len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %v\nwant  %s", calls, want)
	}
}

func TestStopPassesServicesAfterTheSubcommand(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{}, nil
	}}
	_, err, calls := runLifecycle(t, runner, func(c *Compose) (stack.OpResult, error) {
		return c.Stop(context.Background(), lifecycleTarget(), []string{"web"})
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	want := "docker compose -f /p/compose.yaml -f /p/compose.override.yaml --env-file /p/custom.env --profile dev -p hello-world stop web"
	if calls[0] != want {
		t.Fatalf("call = %q, want %q", calls[0], want)
	}
}

func TestDownIsProjectWideAndNeverRemovesVolumes(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{}, nil
	}}
	_, err, calls := runLifecycle(t, runner, func(c *Compose) (stack.OpResult, error) {
		return c.Down(context.Background(), lifecycleTarget())
	})
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	want := "docker compose -f /p/compose.yaml -f /p/compose.override.yaml --env-file /p/custom.env --profile dev -p hello-world down"
	if calls[0] != want {
		t.Fatalf("call = %q, want %q", calls[0], want)
	}
	for _, forbidden := range []string{" -v", "--volumes", "--rmi", "--remove-orphans", "prune"} {
		if strings.Contains(calls[0], forbidden) {
			t.Fatalf("down used %q: %s", forbidden, calls[0])
		}
	}
}

func TestRestartOnlyIsolatesExplicitServices(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{}, nil
	}}
	whole, err, calls := runLifecycle(t, runner, func(c *Compose) (stack.OpResult, error) {
		return c.Restart(context.Background(), lifecycleTarget(), nil)
	})
	_ = whole
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if !strings.HasSuffix(calls[0], " restart") {
		t.Fatalf("whole-stack restart = %q", calls[0])
	}
	_, err, calls = runLifecycle(t, runner, func(c *Compose) (stack.OpResult, error) {
		return c.Restart(context.Background(), lifecycleTarget(), []string{"web"})
	})
	if err != nil {
		t.Fatalf("Restart service: %v", err)
	}
	if last := calls[len(calls)-1]; !strings.HasSuffix(last, " restart --no-deps web") {
		t.Fatalf("service restart = %q", last)
	}
}

func TestLifecycleFailureReturnsToolOutputAndError(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("Error response from daemon: boom"), ExitCode: 1}, nil
	}}
	res, err, _ := runLifecycle(t, runner, func(c *Compose) (stack.OpResult, error) {
		return c.Up(context.Background(), lifecycleTarget(), nil)
	})
	if err == nil {
		t.Fatal("non-zero Compose exit was not an error")
	}
	if !strings.Contains(res.Output, "boom") {
		t.Fatalf("output = %q", res.Output)
	}
}

func TestLifecycleCancellationReturnsContextError(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{}, context.Canceled
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err, _ := runLifecycle(t, runner, func(c *Compose) (stack.OpResult, error) {
		return c.Up(ctx, lifecycleTarget(), nil)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestConfigInfoReadsOnlyNameAndServices(t *testing.T) {
	config := `{"name":"hello-world","services":{"web":{"image":"nginx"},"api":{"image":"api"}},"environment":{"SECRET_TOKEN":"s3cr3t"}}`
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		if strings.HasSuffix(strings.Join(cmd.Args, " "), " config --format json") {
			return execx.Result{Stdout: []byte(config)}, nil
		}
		return execx.Result{Stderr: []byte("unexpected"), ExitCode: 9}, nil
	}}
	info, err := New(runner, nil).ConfigInfo(context.Background(), lifecycleTarget())
	if err != nil {
		t.Fatalf("ConfigInfo: %v", err)
	}
	if info.EffectiveName != "hello-world" {
		t.Fatalf("name = %q", info.EffectiveName)
	}
	if strings.Join(info.Services, ",") != "api,web" {
		t.Fatalf("services = %v", info.Services)
	}
	if strings.Contains(strings.Join(runner.CallLines(), " "), "config --services") {
		t.Fatal("ConfigInfo made an extra services call")
	}
}

func TestConfigInfoClassifiesMissingConfiguration(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("no configuration file provided: not found"), ExitCode: 1}, nil
	}}
	_, err := New(runner, nil).ConfigInfo(context.Background(), lifecycleTarget())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryNoConfiguration {
		t.Fatalf("err = %v", err)
	}
}

func TestDerivedProjectNameUsesAProjectDirectoryProbe(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "leaked-declaration")
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		if cmd.Stdin == nil {
			t.Fatalf("probe did not feed a Compose document: %v", cmd.Args)
		}
		for _, entry := range cmd.Env {
			if strings.HasPrefix(entry, "COMPOSE_PROJECT_NAME=") {
				t.Fatalf("probe inherited a declared name: %q", entry)
			}
		}
		return execx.Result{Stdout: []byte(`{"name":"hello-world"}`)}, nil
	}}
	got, err := New(runner, nil).DerivedProjectName(context.Background(), lifecycleTarget())
	if err != nil {
		t.Fatalf("DerivedProjectName: %v", err)
	}
	if got != "hello-world" {
		t.Fatalf("name = %q", got)
	}
	want := "docker compose --env-file /dev/null -f - --project-directory /p config --format json"
	if calls := runner.CallLines(); len(calls) != 1 || calls[0] != want {
		t.Fatalf("calls = %v\nwant  %s", calls, want)
	}
}

func TestDerivedProjectNameRejectsAMissingName(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte(`{"services":{}}`)}, nil
	}}
	if _, err := New(runner, nil).DerivedProjectName(context.Background(), lifecycleTarget()); err == nil {
		t.Fatal("empty derived name accepted")
	}
}

func TestProjectsParsesExistingProjectsAndOwnershipDirectories(t *testing.T) {
	list := `[{"Name":"hello-world","Status":"running(1)","ConfigFiles":"/work/a/hello-world/compose.yaml"},` +
		`{"Name":"other","Status":"exited(1)","ConfigFiles":"/work/b/other/docker-compose.yml,/work/b/other/override.yml"}]`
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte(list)}, nil
	}}
	projects, err := New(runner, nil).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %+v", projects)
	}
	if projects[0].Name != "hello-world" || projects[0].ProjectDir != "/work/a/hello-world" {
		t.Fatalf("project 0 = %+v", projects[0])
	}
	if projects[1].ProjectDir != "/work/b/other" || len(projects[1].ConfigFiles) != 2 {
		t.Fatalf("project 1 = %+v", projects[1])
	}
	if calls := runner.CallLines(); calls[0] != "docker compose ls -a --format json" {
		t.Fatalf("call = %q", calls[0])
	}
}

func TestProjectsAcceptsEmptyList(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte("[]\n")}, nil
	}}
	projects, err := New(runner, nil).Projects(context.Background())
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects = %+v", projects)
	}
}

func TestProjectsClassifiesEngineFailure(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("Cannot connect to the Docker daemon at unix:///x. Is the docker daemon running?"), ExitCode: 1}, nil
	}}
	_, err := New(runner, nil).Projects(context.Background())
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Diagnostics[0].Category != core.CategoryEngineUnreachable {
		t.Fatalf("err = %v", err)
	}
}
