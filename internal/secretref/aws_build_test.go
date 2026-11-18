package secretref

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
)

func TestPrepareBuildAWSMaterializesTransientFileAndFinishRemovesIt(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	const sentinel = "build-aws-sentinel-7c1"
	runner := awsRunner(awsQuote(t, `{"token":"`+sentinel+`"}`))
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: aws
    ref: build/token
    field: token
    region: us-east-1
    target: BUILD_TOKEN
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=BUILD_TOKEN cat /run/secrets/BUILD_TOKEN > /out\n")
	rt := awsTestRuntime(t, project, state, runner)

	session, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web"))
	if err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	if calls := runner.CallLines(); len(calls) != 1 {
		t.Fatalf("aws calls = %v, want 1", calls)
	}
	path, err := rt.BuildMaterializedPath(targetFor(project), Reference{
		Service: "web", Phase: PhaseBuild, Delivery: DeliveryFile, Source: SourceAWS, Ref: "build/token", Target: "BUILD_TOKEN",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read build materialization: %v", err)
	}
	if string(data) != sentinel {
		t.Fatalf("build materialization = %q", data)
	}
	override := string(mustRead(t, session.Files[0]))
	if !strings.Contains(override, "BUILD_TOKEN") || !strings.Contains(override, "build:") {
		t.Fatalf("override missing the BuildKit mount:\n%s", override)
	}
	if strings.Contains(override, sentinel) {
		t.Fatalf("override leaked the value:\n%s", override)
	}
	if redacted := session.Redact("build log " + sentinel); strings.Contains(redacted, sentinel) {
		t.Fatalf("build redaction left the value: %q", redacted)
	}

	// The AWS value is re-fetched for a build, so its private copy must not
	// outlive the Compose build command like a local file source may.
	if err := session.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("AWS build materialization outlived the build command: %v", err)
	}
	session.Commit()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Commit recreated a transient AWS materialization: %v", err)
	}
}

func TestPrepareBuildAWSAbortRemovesTransientFileAndOverride(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	runner := awsRunner(awsQuote(t, `{"token":"abort-sentinel"}`))
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: aws
    ref: build/token
    field: token
    target: BUILD_TOKEN
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=BUILD_TOKEN cat /run/secrets/BUILD_TOKEN > /out\n")
	rt := awsTestRuntime(t, project, state, runner)
	session, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web"))
	if err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	path, _ := rt.BuildMaterializedPath(targetFor(project), Reference{
		Service: "web", Phase: PhaseBuild, Delivery: DeliveryFile, Source: SourceAWS, Ref: "build/token", Target: "BUILD_TOKEN",
	})
	session.Abort()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("aborted build left the AWS materialization: %v", err)
	}
	override, _ := rt.BuildOverridePath(targetFor(project))
	if _, err := os.Stat(override); !os.IsNotExist(err) {
		t.Fatalf("aborted build left the override: %v", err)
	}
}

func TestPrepareBuildAWSFetchesSharedSecretOnce(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	runner := awsRunner(awsQuote(t, `{"token":"shared-sentinel"}`))
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: aws
    ref: build/shared
    field: token
    target: WEB_TOKEN
  - service: worker
    phase: build
    delivery: file
    source: aws
    ref: build/shared
    field: token
    target: WORKER_TOKEN
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=WEB_TOKEN cat /run/secrets/WEB_TOKEN\n")
	writeDockerfile(t, project, "worker", "FROM busybox\nRUN --mount=type=secret,id=WORKER_TOKEN cat /run/secrets/WORKER_TOKEN\n")
	rt := awsTestRuntime(t, project, state, runner)
	if _, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web", "worker")); err != nil {
		t.Fatalf("PrepareBuild: %v", err)
	}
	if calls := runner.CallLines(); len(calls) != 1 {
		t.Fatalf("aws calls = %v, want one per distinct secret per operation", calls)
	}
}

func TestPrepareBuildAWSFailureChangesNothing(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	runner := &execxtest.Runner{Handler: func(execx.Command) (execx.Result, error) {
		return execx.Result{
			Stderr:   []byte("An error occurred (AccessDeniedException) when calling the GetSecretValue operation"),
			ExitCode: 254,
		}, nil
	}}
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: aws
    ref: build/token
    target: BUILD_TOKEN
`)
	writeDockerfile(t, project, "web", "FROM busybox\nRUN --mount=type=secret,id=BUILD_TOKEN cat /run/secrets/BUILD_TOKEN\n")
	rt := awsTestRuntime(t, project, state, runner)
	_, err := rt.PrepareBuild(context.Background(), targetFor(project), nil, buildHints(project, "web"))
	requireCategory(t, err, core.CategorySecretAWSAccessDenied)
	override, _ := rt.BuildOverridePath(targetFor(project))
	if _, statErr := os.Stat(override); statErr == nil {
		t.Fatalf("failed preparation left the build override")
	}
	entries, _ := os.ReadDir(filepath.Join(state, "stacks", "demo", "build-secrets"))
	if len(entries) > 0 {
		t.Fatalf("failed preparation left materialized build secrets: %v", entries)
	}
}

func TestRuntimePrepareNeverFetchesBuildAWSReferences(t *testing.T) {
	project := t.TempDir()
	state := t.TempDir()
	runner := awsRunner(awsQuote(t, "unused"))
	writeConfig(t, project, `secrets:
  - service: web
    phase: build
    delivery: file
    source: aws
    ref: build/token
    target: BUILD_TOKEN
`)
	rt := awsTestRuntime(t, project, state, runner)
	if _, err := rt.Prepare(context.Background(), targetFor(project), nil, ModeCreate); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if calls := runner.CallLines(); len(calls) != 0 {
		t.Fatalf("runtime delivery fetched a build-phase AWS reference: %v", calls)
	}
}
