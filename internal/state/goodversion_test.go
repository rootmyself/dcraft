package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

func privateStateRootDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("chmod private state dir: %v", err)
	}
	return dir
}

func goodVersionFixture() GoodVersion {
	return GoodVersion{
		Version:     GoodVersionVersion,
		RecordedAt:  time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
		OperationID: "op_fixture",
		Target: GoodTarget{
			ProjectDir:  "/work/app",
			Files:       []string{"/work/app/compose.yaml", "/work/app/compose.prod.yaml"},
			EnvFiles:    []string{"/work/app/.env.prod"},
			Profiles:    []string{"prod"},
			ProjectName: "app-prod",
			Environment: "prod",
			SecretRefs: []SecretReference{{
				Service: "web", Phase: "runtime", Delivery: "env",
				Source: "env", Name: "APP_TOKEN",
			}},
		},
		Services: []GoodService{{
			Service: "web", ImageID: "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			ImageRef: "dcraft-test-web", RetentionTag: "dcraft-good/app-prod-web:1111111111111111",
			Build: true, Healthcheck: true,
		}},
	}
}

func TestGoodVersionRoundTripKeepsOnlyReferences(t *testing.T) {
	store := New(privateStateRootDir(t))
	target := core.Target{ProjectDir: "/work/app", Environment: "prod"}

	if _, ok, err := store.LoadGoodVersion(target); err != nil || ok {
		t.Fatalf("missing record: ok=%v err=%v", ok, err)
	}
	want := goodVersionFixture()
	if err := store.SaveGoodVersion(context.Background(), target, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, ok, err := store.LoadGoodVersion(target)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.OperationID != want.OperationID || got.Target.ProjectName != "app-prod" {
		t.Fatalf("record = %+v", got)
	}
	if len(got.Target.Files) != 2 || got.Target.Files[0] != want.Target.Files[0] || got.Target.Files[1] != want.Target.Files[1] {
		t.Fatalf("file order lost: %v", got.Target.Files)
	}
	if len(got.Services) != 1 || got.Services[0].ImageID != want.Services[0].ImageID ||
		got.Services[0].RetentionTag != want.Services[0].RetentionTag {
		t.Fatalf("services = %+v", got.Services)
	}
	if len(got.Target.SecretRefs) != 1 || got.Target.SecretRefs[0].Name != "APP_TOKEN" {
		t.Fatalf("secret refs = %+v", got.Target.SecretRefs)
	}

	path, err := store.GoodVersionPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "state.json" {
		t.Fatalf("record path = %q, want state.json in the stack dir", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record permissions = %v err=%v", info.Mode().Perm(), err)
	}
	// Only references are stored: there is no value field at all.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, `"value"`) {
		t.Fatalf("record contains a secret value field:\n%s", text)
	}
	if !strings.Contains(text, `"name": "APP_TOKEN"`) {
		t.Fatalf("record lost the reference name:\n%s", text)
	}
}

func TestGoodVersionReplacementKeepsExactlyOneRecord(t *testing.T) {
	store := New(privateStateRootDir(t))
	target := core.Target{ProjectDir: "/work/app"}

	first := goodVersionFixture()
	if err := store.SaveGoodVersion(context.Background(), target, first); err != nil {
		t.Fatal(err)
	}
	second := goodVersionFixture()
	second.OperationID = "op_second"
	second.Services[0].ImageID = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	if err := store.SaveGoodVersion(context.Background(), target, second); err != nil {
		t.Fatal(err)
	}

	got, ok, err := store.LoadGoodVersion(target)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got.OperationID != "op_second" {
		t.Fatalf("record was not replaced: %+v", got)
	}
	path, _ := store.GoodVersionPath(target)
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var records int
	for _, entry := range entries {
		if entry.Name() == "state.json" {
			records++
		}
	}
	if records != 1 {
		t.Fatalf("state.json count = %d, want exactly one previous good deployment", records)
	}
}

func TestGoodVersionIsolatedPerEnvironmentAndFolder(t *testing.T) {
	store := New(privateStateRootDir(t))
	base := goodVersionFixture()

	prod := core.Target{ProjectDir: "/work/app", Environment: "prod"}
	dev := core.Target{ProjectDir: "/work/app", Environment: "dev"}
	other := core.Target{ProjectDir: "/work/other", Environment: "prod"}

	if err := store.SaveGoodVersion(context.Background(), prod, base); err != nil {
		t.Fatal(err)
	}
	for _, target := range []core.Target{dev, other} {
		if _, ok, err := store.LoadGoodVersion(target); err != nil || ok {
			t.Fatalf("target %+v shares the prod record: ok=%v err=%v", target, ok, err)
		}
	}
}

func TestSaveGoodVersionCanceledAtCommitLeavesPreviousRecordByteIdentical(t *testing.T) {
	store := New(privateStateRootDir(t))
	target := core.Target{ProjectDir: "/work/app", Environment: "prod"}
	first := goodVersionFixture()
	if err := store.SaveGoodVersion(context.Background(), target, first); err != nil {
		t.Fatalf("save first: %v", err)
	}
	path, err := store.GoodVersionPath(target)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	second := goodVersionFixture()
	second.OperationID = "op_canceled"
	second.RecordedAt = second.RecordedAt.Add(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.SaveGoodVersion(ctx, target, second); !errors.Is(err, context.Canceled) {
		t.Fatalf("save err = %v, want context.Canceled", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("canceled save replaced the good-version record:\nbefore=%s\nafter=%s", before, after)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".state-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("canceled save left a temp file: %s", entry.Name())
		}
	}
}

func TestGoodVersionRejectsCorruptAndNewerRecords(t *testing.T) {
	store := New(privateStateRootDir(t))
	target := core.Target{ProjectDir: "/work/app"}
	path, err := store.GoodVersionPath(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadGoodVersion(target); err == nil {
		t.Fatal("corrupt record was accepted")
	}

	newer := goodVersionFixture()
	newer.Version = GoodVersionVersion + 1
	data, _ := json.Marshal(newer)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LoadGoodVersion(target); err == nil {
		t.Fatal("newer record version was accepted")
	}
}
