package compose_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/compose"
	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/stack"
)

func inspectTarget(t *testing.T) core.Target {
	t.Helper()
	return core.Target{
		ProjectDir:  t.TempDir(),
		Files:       []string{"/tmp/project/compose.yaml", "/tmp/project/compose.override.yaml"},
		EnvFiles:    []string{"/tmp/project/local.env"},
		Profiles:    []string{"dev"},
		ProjectName: "hello-world",
	}
}

func inspectFake(t *testing.T, configJSON, psNDJSON string) *execxtest.Runner {
	t.Helper()
	return &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		joined := strings.Join(cmd.Args, " ")
		switch {
		case strings.HasSuffix(joined, "config --format json"):
			return execx.Result{Stdout: []byte(configJSON)}, nil
		case strings.HasSuffix(joined, "ps --all --format json"):
			return execx.Result{Stdout: []byte(psNDJSON)}, nil
		}
		return execx.Result{Stderr: []byte("unexpected: " + joined), ExitCode: 99}, nil
	}}
}

func serviceByName(t *testing.T, snap stack.Snapshot, name string) stack.Service {
	t.Helper()
	for _, s := range snap.Services {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("service %q not found in %+v", name, snap.Services)
	return stack.Service{}
}

const configOneService = `{
  "name": "hello-world",
  "services": {
    "web": {
      "image": "nginx:alpine",
      "ports": [{"mode":"ingress","target":80,"published":"8080","protocol":"tcp"}]
    }
  }
}`

const psOneRunningNoHealth = `{"Name":"hello-world-web-1","Service":"web","State":"running","Health":"","Image":"nginx:alpine","ExitCode":0,"Project":"hello-world","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}`

func TestInspectRunningWithoutHealthcheckIsUnverified(t *testing.T) {
	target := inspectTarget(t)
	ins := compose.New(inspectFake(t, configOneService, psOneRunningNoHealth), envMap(nil).getenv)

	snap, err := ins.Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if snap.Backend != "compose" {
		t.Fatalf("Backend = %q", snap.Backend)
	}
	if snap.ProjectName != "hello-world" {
		t.Fatalf("ProjectName = %q", snap.ProjectName)
	}
	web := serviceByName(t, snap, "web")
	if web.State != stack.StateRunning {
		t.Fatalf("state = %q, want running", web.State)
	}
	if web.Health != stack.HealthNone {
		t.Fatalf("health = %q, want none (no healthcheck)", web.Health)
	}
	if web.HealthcheckConfigured {
		t.Fatal("healthcheck_configured = true, want false")
	}
	if web.AppVerified {
		t.Fatal("app_verified = true without a healthcheck; running must not imply verified")
	}
	if len(web.Instances) != 1 || web.Instances[0].Name != "hello-world-web-1" {
		t.Fatalf("instances = %+v", web.Instances)
	}
	if len(web.Connections) != 1 || web.Connections[0].Source != stack.SourceRuntime {
		t.Fatalf("connections = %+v", web.Connections)
	}
	c := web.Connections[0]
	if c.PublishedPort != 8080 || c.TargetPort != 80 || c.Protocol != "tcp" || c.HostIP != "0.0.0.0" {
		t.Fatalf("connection = %+v", c)
	}
	if snap.Summary.Running != 1 || snap.Summary.Healthy != 0 || snap.Summary.Unverified != 1 {
		t.Fatalf("summary = %+v", snap.Summary)
	}
}

func TestInspectHealthyServiceIsVerified(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"hello-world","services":{"web":{"image":"nginx:alpine","healthcheck":{"test":["CMD","true"]}}}}`
	ps := `{"Name":"hello-world-web-1","Service":"web","State":"running","Health":"healthy","Image":"nginx:alpine","ExitCode":0}`
	snap, err := compose.New(inspectFake(t, config, ps), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	web := serviceByName(t, snap, "web")
	if !web.HealthcheckConfigured {
		t.Fatal("healthcheck_configured = false, want true")
	}
	if web.Health != stack.HealthHealthy || !web.AppVerified {
		t.Fatalf("health = %q verified=%v, want healthy/true", web.Health, web.AppVerified)
	}
	if snap.Summary.Healthy != 1 || snap.Summary.Unverified != 0 {
		t.Fatalf("summary = %+v", snap.Summary)
	}
}

func TestInspectHealthStatesDistinguishStartingUnhealthyAndDisabled(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"p","services":{
	  "starting":{"image":"a","healthcheck":{"test":["CMD","true"]}},
	  "bad":{"image":"b","healthcheck":{"test":["CMD","false"]}},
	  "disabled":{"image":"c","healthcheck":{"disable":true}}
	}}`
	ps := strings.Join([]string{
		`{"Name":"p-starting-1","Service":"starting","State":"running","Health":"starting","Image":"a"}`,
		`{"Name":"p-bad-1","Service":"bad","State":"running","Health":"unhealthy","Image":"b"}`,
		`{"Name":"p-disabled-1","Service":"disabled","State":"running","Health":"","Image":"c"}`,
	}, "\n")
	snap, err := compose.New(inspectFake(t, config, ps), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if s := serviceByName(t, snap, "starting"); s.Health != stack.HealthStarting || s.AppVerified {
		t.Fatalf("starting health = %q verified=%v", s.Health, s.AppVerified)
	}
	if s := serviceByName(t, snap, "bad"); s.Health != stack.HealthUnhealthy || s.AppVerified {
		t.Fatalf("bad health = %q verified=%v", s.Health, s.AppVerified)
	}
	if s := serviceByName(t, snap, "disabled"); s.HealthcheckConfigured || s.AppVerified {
		t.Fatalf("disabled healthcheck should not be configured: %+v", s)
	}
	if snap.Summary.Unhealthy != 1 {
		t.Fatalf("unhealthy summary = %d, want 1", snap.Summary.Unhealthy)
	}
}

func TestInspectExitedServiceAndMissingService(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"p","services":{
	  "db":{"image":"postgres:16"},
	  "cache":{"image":"redis:7"}
	}}`
	ps := `{"Name":"p-db-1","Service":"db","State":"exited","Health":"","Image":"postgres:16","ExitCode":137}`
	snap, err := compose.New(inspectFake(t, config, ps), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	db := serviceByName(t, snap, "db")
	if db.State != stack.StateExited {
		t.Fatalf("db state = %q, want exited", db.State)
	}
	if len(db.Instances) != 1 || db.Instances[0].ExitCode != 137 {
		t.Fatalf("db instances = %+v", db.Instances)
	}
	cache := serviceByName(t, snap, "cache")
	if cache.State != stack.StateNotCreated || len(cache.Instances) != 0 {
		t.Fatalf("cache = %+v", cache)
	}
	if snap.Summary.Stopped != 1 || snap.Summary.NotCreated != 1 {
		t.Fatalf("summary = %+v", snap.Summary)
	}
}

func TestInspectMultipleInstancesRequireAllHealthy(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"p","services":{"web":{"image":"nginx","healthcheck":{"test":["CMD","true"]}}}}`
	ps := strings.Join([]string{
		`{"Name":"p-web-1","Service":"web","State":"running","Health":"healthy","Image":"nginx"}`,
		`{"Name":"p-web-2","Service":"web","State":"running","Health":"starting","Image":"nginx"}`,
	}, "\n")
	snap, err := compose.New(inspectFake(t, config, ps), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	web := serviceByName(t, snap, "web")
	if web.Health != stack.HealthStarting {
		t.Fatalf("health = %q, want starting", web.Health)
	}
	if web.AppVerified {
		t.Fatal("service with a starting replica must not be app_verified")
	}
	if len(web.Instances) != 2 {
		t.Fatalf("instances = %d, want 2", len(web.Instances))
	}
}

func TestInspectUnconfiguredInstanceIsShownAsNotConfigured(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"p","services":{"web":{"image":"nginx"}}}`
	ps := strings.Join([]string{
		`{"Name":"p-web-1","Service":"web","State":"running","Health":"","Image":"nginx"}`,
		`{"Name":"p-oldtool-1","Service":"oldtool","State":"running","Health":"","Image":"alpine"}`,
	}, "\n")
	snap, err := compose.New(inspectFake(t, config, ps), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	old := serviceByName(t, snap, "oldtool")
	if old.Configured {
		t.Fatal("oldtool configured = true, want false")
	}
	if old.State != stack.StateRunning {
		t.Fatalf("oldtool state = %q", old.State)
	}
	if snap.Summary.Services != 2 || snap.Summary.Instances != 2 {
		t.Fatalf("summary = %+v", snap.Summary)
	}
}

func TestInspectConfiguredPortsForStoppedAndDynamicPorts(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"p","services":{
	  "web":{"image":"nginx","ports":[
	    {"mode":"ingress","target":80,"published":"8080","protocol":"tcp"},
	    {"mode":"ingress","host_ip":"127.0.0.1","target":443,"protocol":"tcp"}
	  ]}
	}}`
	snap, err := compose.New(inspectFake(t, config, ""), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	web := serviceByName(t, snap, "web")
	if len(web.Connections) != 2 {
		t.Fatalf("connections = %+v", web.Connections)
	}
	byTarget := map[int]stack.Connection{}
	for _, c := range web.Connections {
		byTarget[c.TargetPort] = c
	}
	if c := byTarget[80]; c.Source != stack.SourceConfig || c.PublishedPort != 8080 {
		t.Fatalf("config port 80 = %+v", c)
	}
	if c := byTarget[443]; c.HostIP != "127.0.0.1" || c.PublishedPort != 0 {
		t.Fatalf("dynamic config port 443 = %+v", c)
	}
}

func TestInspectBuildOnlyService(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"p","services":{"api":{"build":{"context":"."}}}}`
	snap, err := compose.New(inspectFake(t, config, ""), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	api := serviceByName(t, snap, "api")
	if !api.Build || api.Image != "" {
		t.Fatalf("api = %+v, want build service", api)
	}
}

func TestInspectNeverLeaksEnvironmentOrSecretValues(t *testing.T) {
	target := inspectTarget(t)
	config := `{"name":"p","services":{"web":{"image":"nginx","environment":{"DB_PASSWORD":"super-secret-value","TOKEN":"another-secret"}}}}`
	ps := `{"Name":"p-web-1","Service":"web","State":"running","Health":"healthy","Image":"nginx","Labels":"secret=super-secret-value"}`
	snap, err := compose.New(inspectFake(t, config, ps), nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret-value", "another-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("snapshot leaked secret %q: %s", secret, data)
		}
	}
}

func TestInspectPassesComposeInputsConsistently(t *testing.T) {
	target := inspectTarget(t)
	fake := inspectFake(t, configOneService, psOneRunningNoHealth)
	_, err := compose.New(fake, nil).Inspect(context.Background(), target)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 2 {
		t.Fatalf("calls = %v", fake.CallLines())
	}
	for _, call := range calls {
		wantPrefix := []string{"compose", "-f", target.Files[0], "-f", target.Files[1], "--env-file", target.EnvFiles[0], "--profile", "dev", "-p", "hello-world"}
		if !reflect.DeepEqual(call.Args[:len(wantPrefix)], wantPrefix) {
			t.Fatalf("args = %v, want prefix %v", call.Args, wantPrefix)
		}
		if call.Dir != target.ProjectDir {
			t.Fatalf("dir = %q, want %q", call.Dir, target.ProjectDir)
		}
	}
}

func TestInspectConfigErrorClasses(t *testing.T) {
	target := inspectTarget(t)
	cases := []struct {
		name     string
		result   execx.Result
		category core.Category
	}{
		{"no file", execx.Result{Stderr: []byte("no configuration file provided: not found"), ExitCode: 1}, core.CategoryNoConfiguration},
		{"invalid yaml", execx.Result{Stderr: []byte("failed to parse compose.yaml: yaml: did not find expected key"), ExitCode: 1}, core.CategoryInvalidConfiguration},
		{"unknown flag", execx.Result{Stderr: []byte("unknown flag: --format"), ExitCode: 1}, core.CategoryComposeUnsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) { return tc.result, nil }}
			_, err := compose.New(fake, nil).Inspect(context.Background(), target)
			if err == nil {
				t.Fatal("Inspect succeeded, want error")
			}
			var ce *core.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error %v is not *core.Error", err)
			}
			if ce.Diagnostics[0].Category != tc.category {
				t.Fatalf("category = %q, want %q", ce.Diagnostics[0].Category, tc.category)
			}
			if len(ce.Diagnostics[0].NextActions) == 0 {
				t.Fatal("missing Korean next actions")
			}
		})
	}
}

func TestInspectPSEngineFailureIsNotMisreportedAsConfigurationError(t *testing.T) {
	target := inspectTarget(t)
	fake := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		if strings.HasSuffix(strings.Join(cmd.Args, " "), "config --format json") {
			return execx.Result{Stdout: []byte(configOneService)}, nil
		}
		return execx.Result{Stderr: []byte("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"), ExitCode: 1}, nil
	}}
	_, err := compose.New(fake, nil).Inspect(context.Background(), target)
	if err == nil {
		t.Fatal("Inspect succeeded, want error")
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *core.Error", err)
	}
	if ce.Diagnostics[0].Category != core.CategoryEngineUnreachable {
		t.Fatalf("category = %q, want engine_unreachable", ce.Diagnostics[0].Category)
	}
}

func TestInspectPSMalformedJSONIsInternalError(t *testing.T) {
	target := inspectTarget(t)
	fake := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		if strings.HasSuffix(strings.Join(cmd.Args, " "), "config --format json") {
			return execx.Result{Stdout: []byte(configOneService)}, nil
		}
		return execx.Result{Stdout: []byte("not json")}, nil
	}}
	_, err := compose.New(fake, nil).Inspect(context.Background(), target)
	if err == nil {
		t.Fatal("Inspect succeeded, want error")
	}
	var ce *core.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not *core.Error", err)
	}
	if ce.Diagnostics[0].Category != core.CategoryInternal {
		t.Fatalf("category = %q, want internal_error", ce.Diagnostics[0].Category)
	}
}
