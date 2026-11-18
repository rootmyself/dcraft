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

func updateTarget() core.Target {
	return core.Target{
		ProjectDir:  "/work/app",
		Files:       []string{"/work/app/compose.yaml"},
		EnvFiles:    []string{"/work/app/.env.dev"},
		Profiles:    []string{"dev"},
		ProjectName: "app-dev",
	}
}

func TestUpdateModelReadsServicesWithoutEnvironment(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte(`{
			"name": "resolved-name",
			"services": {
				"web": {"image": "nginx:1.27", "healthcheck": {"test": ["CMD","true"]}},
				"worker": {"image": "worker:local", "healthcheck": {"disable": true}},
				"builder": {"build": {"context": "."}, "healthcheck": {"test": ["CMD","true"]}}
			}
		}`)}, nil
	}}
	c := New(runner, nil)

	model, err := c.UpdateModel(context.Background(), updateTarget())
	if err != nil {
		t.Fatal(err)
	}
	if model.ProjectName != "resolved-name" {
		t.Fatalf("project name = %q", model.ProjectName)
	}
	if len(model.Services) != 3 {
		t.Fatalf("services = %+v", model.Services)
	}
	byName := map[string]stack.ServiceDefinition{}
	for _, svc := range model.Services {
		byName[svc.Name] = svc
	}
	if got := byName["web"]; got.Image != "nginx:1.27" || got.Build || !got.Healthcheck {
		t.Fatalf("web = %+v", got)
	}
	if got := byName["worker"]; got.Image != "worker:local" || got.Build || got.Healthcheck {
		t.Fatalf("worker = %+v, want a disabled healthcheck to count as none", got)
	}
	if got := byName["builder"]; got.Image != "" || !got.Build || !got.Healthcheck {
		t.Fatalf("builder = %+v", got)
	}

	line := runner.CallLines()[0]
	for _, part := range []string{"compose", "-f /work/app/compose.yaml", "--env-file /work/app/.env.dev", "--profile dev", "-p app-dev", "config --format json"} {
		if !strings.Contains(line, part) {
			t.Fatalf("config call %q misses %q", line, part)
		}
	}
	if strings.Contains(strings.Join(runner.CallLines(), " "), "environment") {
		t.Fatalf("update asked for environment values: %v", runner.CallLines())
	}
}

func TestContainersReadsExactImageIdentityAndHealth(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		switch {
		case strings.HasSuffix(strings.Join(cmd.Args, " "), "ps -a -q"):
			return execx.Result{Stdout: []byte("aaa\nbbb\n")}, nil
		case len(cmd.Args) > 0 && cmd.Args[0] == "inspect":
			if len(cmd.Args) != 3 {
				t.Fatalf("inspect args = %v", cmd.Args)
			}
			return execx.Result{Stdout: []byte(`[
				{"Id":"aaa","Image":"sha256:1111","Created":"2026-11-18T10:00:00Z","RestartCount":0,
				 "State":{"Status":"running","Running":true,"Restarting":false,"ExitCode":0,
				          "Health":{"Status":"healthy","FailingStreak":0}},
				 "Config":{"Image":"nginx:1.27","Labels":{"com.docker.compose.service":"web"}}},
				{"Id":"bbb","Image":"sha256:2222","Created":"2026-11-18T10:01:00Z","RestartCount":9,
				 "State":{"Status":"restarting","Running":false,"Restarting":true,"ExitCode":1},
				 "Config":{"Image":"worker:local","Labels":{"com.docker.compose.service":"worker"}}}
			]`)}, nil
		}
		t.Fatalf("unexpected call %v", cmd.Args)
		return execx.Result{}, nil
	}}
	c := New(runner, nil)

	containers, err := c.Containers(context.Background(), updateTarget())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 2 {
		t.Fatalf("containers = %+v", containers)
	}
	web, worker := containers[0], containers[1]
	if web.Service != "web" || web.ImageID != "sha256:1111" || web.ImageRef != "nginx:1.27" ||
		!web.Running || web.Health != "healthy" || !web.HasHealth || web.RestartCount != 0 {
		t.Fatalf("web = %+v", web)
	}
	if worker.Service != "worker" || worker.State != "restarting" || !worker.Restarting ||
		worker.ExitCode != 1 || worker.RestartCount != 9 || worker.HasHealth {
		t.Fatalf("worker = %+v", worker)
	}
	if !strings.Contains(runner.CallLines()[0], "ps -a -q") {
		t.Fatalf("first call = %q", runner.CallLines()[0])
	}
}

func TestContainersHandlesEmptyProject(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		if strings.HasSuffix(strings.Join(cmd.Args, " "), "ps -a -q") {
			return execx.Result{}, nil
		}
		t.Fatalf("unexpected inspect for an empty project: %v", cmd.Args)
		return execx.Result{}, nil
	}}
	c := New(runner, nil)
	containers, err := c.Containers(context.Background(), updateTarget())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 0 {
		t.Fatalf("containers = %+v", containers)
	}
}

func TestUpdateBuildPullApplyArguments(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte("ok")}, nil
	}}
	c := New(runner, nil)
	ctx := context.Background()
	target := updateTarget()

	if _, err := c.Build(ctx, target, []string{"api", "web"}, stack.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Build(ctx, target, []string{"api"}, stack.BuildOptions{NoCache: true, Pull: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Pull(ctx, target, []string{"db"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, target, nil, stack.ApplyOptions{PullPolicy: "missing"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Apply(ctx, target, []string{"web"}, stack.ApplyOptions{ForceRecreate: true, PullPolicy: "never"}); err != nil {
		t.Fatal(err)
	}

	lines := runner.CallLines()
	if len(lines) != 5 {
		t.Fatalf("calls = %v", lines)
	}
	if !strings.Contains(lines[0], "build") || strings.Contains(lines[0], "--no-cache") || strings.Contains(lines[0], "--pull") {
		t.Fatalf("cached build = %q", lines[0])
	}
	if !strings.HasSuffix(lines[0], " build api web") {
		t.Fatalf("build services = %q", lines[0])
	}
	if !strings.Contains(lines[1], "build --no-cache --pull api") {
		t.Fatalf("explicit build choices = %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], " pull db") {
		t.Fatalf("pull = %q", lines[2])
	}
	if !strings.Contains(lines[3], " up --detach --pull missing") || strings.Contains(lines[3], "--force-recreate") {
		t.Fatalf("ordinary apply = %q", lines[3])
	}
	if !strings.Contains(lines[4], " up --detach --force-recreate --pull never web") {
		t.Fatalf("forced apply = %q", lines[4])
	}
	for _, line := range lines {
		if strings.Contains(line, "--wait") {
			t.Fatalf("update delegated readiness to an untrusted --wait: %q", line)
		}
	}
}

func TestImageIDAndTagImage(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		switch {
		case cmd.Args[0] == "image" && len(cmd.Args) > 1 && cmd.Args[1] == "inspect":
			ref := cmd.Args[2]
			if strings.Contains(ref, "missing") {
				return execx.Result{Stderr: []byte("Error response from daemon: No such image: " + ref), ExitCode: 1}, nil
			}
			return execx.Result{Stdout: []byte("sha256:abc\n")}, nil
		case cmd.Args[0] == "tag":
			return execx.Result{}, nil
		}
		t.Fatalf("unexpected call %v", cmd.Args)
		return execx.Result{}, nil
	}}
	c := New(runner, nil)

	id, err := c.ImageID(context.Background(), "dcraft-test:latest")
	if err != nil || id != "sha256:abc" {
		t.Fatalf("image id = %q err=%v", id, err)
	}
	var missing *stack.ImageMissingError
	if _, err := c.ImageID(context.Background(), "dcraft-missing:none"); !errors.As(err, &missing) {
		t.Fatalf("missing image error = %v, want *stack.ImageMissingError", err)
	}
	if err := c.TagImage(context.Background(), "sha256:abc", "dcraft-good/app-web:abc"); err != nil {
		t.Fatal(err)
	}
	lines := runner.CallLines()
	if !strings.Contains(lines[2], "tag sha256:abc dcraft-good/app-web:abc") {
		t.Fatalf("tag call = %q", lines[2])
	}
}

func TestImageIDClassifiesEngineFailure(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("Cannot connect to the Docker daemon at unix:///x. Is the docker daemon running?"), ExitCode: 1}, nil
	}}
	c := New(runner, nil)
	_, err := c.ImageID(context.Background(), "dcraft-test:latest")
	var missing *stack.ImageMissingError
	if errors.As(err, &missing) {
		t.Fatalf("engine failure was classified as a missing image: %v", err)
	}
	var ce *core.Error
	if !errors.As(err, &ce) || len(ce.Diagnostics) == 0 || ce.Diagnostics[0].Category != core.CategoryEngineUnreachable {
		t.Fatalf("diagnostic = %v", err)
	}
}
