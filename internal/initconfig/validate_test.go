package initconfig

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

func TestComposeValidatorUsesStdinAndTheDestinationWorkingDirectory(t *testing.T) {
	var got execx.Command
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		got = cmd
		return execx.Result{Stdout: []byte(`{
			"name":"hello-world",
			"services":{"app":{"image":"busybox:1.36"}},
			"networks":{"default":{"name":"hello-world_default"}},
			"volumes":{"cache":{"name":"hello-world_cache"}}
		}`)}, nil
	}}
	validator := ComposeValidator{Runner: runner}
	content := []byte("services:\n  app:\n    image: busybox:1.36\n")
	result, err := validator.Validate(context.Background(), "/project", content)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.Name != "docker" || strings.Join(got.Args, " ") != "compose -f - config --format json" {
		t.Fatalf("command = %s %v", got.Name, got.Args)
	}
	if got.Dir != "/project" {
		t.Fatalf("working directory = %q, want /project", got.Dir)
	}
	if got.Stdin == nil {
		t.Fatal("content was not passed on stdin")
	}
	buf := make([]byte, len(content))
	if _, err := got.Stdin.Read(buf); err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	if string(buf) != string(content) {
		t.Fatalf("stdin = %q", buf)
	}
	if result.ProjectName != "hello-world" || result.DefaultNetwork != "hello-world_default" {
		t.Fatalf("validation = %+v", result)
	}
	if result.Volumes["cache"] != "hello-world_cache" {
		t.Fatalf("resolved volumes = %v", result.Volumes)
	}
	if len(result.Services) != 1 || result.Services[0] != "app" {
		t.Fatalf("services = %v", result.Services)
	}
}

func TestComposeValidatorRejectsComposeErrorsWithoutSaving(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("services.app.bogus_key false schema"), ExitCode: 1}, nil
	}}
	_, err := ComposeValidator{Runner: runner}.Validate(context.Background(), "/project", []byte("services: {}\n"))
	if err == nil {
		t.Fatal("invalid output was accepted")
	}
	ce, ok := err.(*core.Error)
	if !ok || len(ce.Diagnostics) == 0 {
		t.Fatalf("error = %T %v, want *core.Error", err, err)
	}
	d := ce.Diagnostics[0]
	if d.Category != core.CategoryInitInvalid {
		t.Fatalf("category = %q, want init_invalid", d.Category)
	}
	if !strings.Contains(d.Detail, "bogus_key") {
		t.Fatalf("detail = %q, want the Compose error", d.Detail)
	}
}

func TestComposeValidatorRejectsMissingProjectNameAndMalformedJSON(t *testing.T) {
	cases := []string{
		`{"services":{"app":{}}}`,
		`not json`,
		`{"name":"hello","services":[]}`,
	}
	for _, output := range cases {
		runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
			return execx.Result{Stdout: []byte(output)}, nil
		}}
		_, err := ComposeValidator{Runner: runner}.Validate(context.Background(), "/project", []byte("services: {}\n"))
		if err == nil {
			t.Errorf("output %q accepted", output)
			continue
		}
		ce, ok := err.(*core.Error)
		if !ok || ce.Diagnostics[0].Category != core.CategoryInitInvalid {
			t.Errorf("output %q error = %v, want init_invalid", output, err)
		}
	}
}

func TestComposeValidatorClassifiesMissingCompose(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("docker: 'compose' is not a docker command.\nSee 'docker --help'"), ExitCode: 1}, nil
	}}
	_, err := ComposeValidator{Runner: runner}.Validate(context.Background(), "/project", []byte("services: {}\n"))
	ce, ok := err.(*core.Error)
	if !ok || ce.Diagnostics[0].Category != core.CategoryComposeMissing {
		t.Fatalf("error = %v, want compose_missing", err)
	}
}

func TestComposeValidatorClassifiesMissingDockerAndCancellation(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{}, errors.New("exec: \"docker\": executable file not found in $PATH")
	}}
	_, err := ComposeValidator{Runner: runner}.Validate(context.Background(), "/project", []byte("services: {}\n"))
	ce, ok := err.(*core.Error)
	if !ok || ce.Diagnostics[0].Category != core.CategoryDockerMissing {
		t.Fatalf("error = %v, want docker_missing", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ComposeValidator{Runner: runner}.Validate(ctx, "/project", []byte("services: {}\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", err)
	}
}
