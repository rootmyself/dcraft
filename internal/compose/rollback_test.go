package compose

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/execx"
	"github.com/rootmyself/dcraft/internal/execx/execxtest"
	"github.com/rootmyself/dcraft/internal/secretref"
	"github.com/rootmyself/dcraft/internal/stack"
)

type fakeSecretPreparer struct {
	session    secretref.Session
	aborted    bool
	mode       secretref.Mode
	references []secretref.Reference
}

func (f *fakeSecretPreparer) Prepare(_ context.Context, _ core.Target, _ []string, mode secretref.Mode) (secretref.Session, error) {
	f.mode = mode
	return f.session, nil
}

func (f *fakeSecretPreparer) PrepareReferences(_ context.Context, _ core.Target, refs []secretref.Reference, _ []string, mode secretref.Mode) (secretref.Session, error) {
	f.mode = mode
	f.references = refs
	return f.session, nil
}

func (f *fakeSecretPreparer) CheckMaterialized(core.Target, []secretref.Reference) error { return nil }

// PrepareBuild satisfies the build-secret boundary added by ticket #10.
// Rollback never builds, so this fake reports an empty build session.
func (f *fakeSecretPreparer) PrepareBuild(_ context.Context, _ core.Target, _ []string, _ map[string]secretref.BuildServiceHint) (secretref.BuildSession, error) {
	return secretref.BuildSession{}, nil
}

func (f *fakeSecretPreparer) CleanupStack(core.Target) error { return nil }

// RedactText matches the SecretPreparer boundary added by ticket #16. This
// fake fetches no values, so redaction is the identity.
func (f *fakeSecretPreparer) RedactText(_ core.Target, text string) string { return text }

const (
	rbImageA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rbImageB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func rollbackPins() []stack.RollbackPin {
	return []stack.RollbackPin{
		{Service: "web", ImageID: rbImageA, RetentionTag: "dcraft-good/app-web:aaaaaaaaaaaaaaaa"},
		{Service: "db", ImageID: rbImageB, RetentionTag: "dcraft-good/app-db:bbbbbbbbbbbbbbbb"},
	}
}

func TestApplyRollbackPinsExactImagesAndNeverPullsOrBuilds(t *testing.T) {
	var pinPath, pinContent string
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		args := cmd.Args
		if len(args) < 2 || args[0] != "compose" {
			t.Fatalf("unexpected call %v", args)
		}
		// The last -f input is the dcraft-managed pin override.
		for i := 0; i+1 < len(args); i++ {
			if args[i] == "-f" && args[i+1] != "/work/app/compose.yaml" {
				pinPath = args[i+1]
				data, err := os.ReadFile(args[i+1])
				if err != nil {
					t.Fatalf("read pin override: %v", err)
				}
				pinContent = string(data)
			}
		}
		return execx.Result{Stdout: []byte("started")}, nil
	}}
	c := New(runner, nil)
	res, err := c.ApplyRollback(context.Background(), updateTarget(), []string{"web", "db"}, rollbackPins(), nil)
	if err != nil {
		t.Fatalf("apply rollback: %v", err)
	}
	if res.Output != "started" {
		t.Fatalf("output = %q", res.Output)
	}
	line := runner.CallLines()[0]
	for _, want := range []string{
		"compose -f /work/app/compose.yaml",
		"--env-file /work/app/.env.dev",
		"--profile dev",
		"-p app-dev",
		"up --detach --pull never --no-build web db",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("apply call %q misses %q", line, want)
		}
	}
	for _, forbidden := range []string{" pull ", " build ", "--wait", " down ", " prune", "image rm"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("rollback call contains %q: %q", forbidden, line)
		}
	}
	if pinContent == "" {
		t.Fatal("the pin override was not passed to Compose")
	}
	for _, want := range []string{"web:", rbImageA, "db:", rbImageB} {
		if !strings.Contains(pinContent, want) {
			t.Fatalf("pin override misses %q:\n%s", want, pinContent)
		}
	}
	if strings.Contains(pinContent, "dcraft-good/") {
		t.Fatalf("pin override uses a movable tag identity:\n%s", pinContent)
	}
	if _, err := os.Stat(pinPath); !os.IsNotExist(err) {
		t.Fatalf("pin override was not removed after apply: %v", err)
	}
}

func TestApplyRollbackUsesOnlyRecordedSecretReferences(t *testing.T) {
	prep := &fakeSecretPreparer{session: secretref.Session{
		Files:   []string{"/state/stacks/app/compose.secrets.yaml"},
		Env:     []string{"DCRAFT_SECRET_1=value"},
		Notices: []string{"비밀값 전달 적용."},
	}}
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stdout: []byte("started")}, nil
	}}
	c := New(runner, nil)
	c.SetSecretRuntime(prep)

	recorded := []secretref.Reference{{
		Service: "web", Phase: secretref.PhaseRuntime,
		Delivery: secretref.DeliveryEnvironment, Source: secretref.SourceEnv,
		Ref: "DB_PASSWORD", Target: "DB_PASSWORD",
	}}
	res, err := c.ApplyRollback(context.Background(), updateTarget(), []string{"web"}, rollbackPins(), recorded)
	if err != nil {
		t.Fatalf("apply rollback: %v", err)
	}
	if prep.mode != secretref.ModeCreate {
		t.Fatalf("secret mode = %v, want create", prep.mode)
	}
	if len(prep.references) != 1 || prep.references[0].Ref != "DB_PASSWORD" {
		t.Fatalf("prepared references = %+v, want the recorded list", prep.references)
	}
	if len(res.Notices) == 0 {
		t.Fatal("secret delivery notices were dropped")
	}
	line := runner.CallLines()[0]
	user := strings.Index(line, "-f /work/app/compose.yaml")
	secret := strings.Index(line, "-f /state/stacks/app/compose.secrets.yaml")
	if user < 0 || secret < 0 || secret < user {
		t.Fatalf("effective Compose file order = %q", line)
	}
}

func TestApplyRollbackFailureReturnsToolOutput(t *testing.T) {
	runner := &execxtest.Runner{Handler: func(cmd execx.Command) (execx.Result, error) {
		return execx.Result{Stderr: []byte("pull access denied for x"), ExitCode: 1}, nil
	}}
	c := New(runner, nil)
	_, err := c.ApplyRollback(context.Background(), updateTarget(), []string{"web"}, rollbackPins(), nil)
	if err == nil || !strings.Contains(err.Error(), "exited with status 1") {
		t.Fatalf("error = %v", err)
	}
}
