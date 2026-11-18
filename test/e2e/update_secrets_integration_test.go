package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/state"
)

// An explicit update must deliver runtime secrets exactly like up: the current
// environment value and file materialization reach a force-recreated container,
// the previous-good record keeps references (never values), and down keeps its
// cleanup lifetime rules.
func TestIntegrationUpdateDeliversRuntimeSecretsAndRecordsReferences(t *testing.T) {
	env := requireIntegration(t)
	projectDir := t.TempDir()
	name := "dcraft-test-upd-secrets-" + randomHex(t, 6)
	stateDir := t.TempDir()
	envSecretA := "sentinel-upd-env-a-" + randomHex(t, 8)
	envSecretB := "sentinel-upd-env-b-" + randomHex(t, 8)
	fileSecretA := "sentinel-upd-file-a-" + randomHex(t, 8)
	fileSecretB := "sentinel-upd-file-b-" + randomHex(t, 8)
	sentinels := []string{envSecretA, envSecretB, fileSecretA, fileSecretB}

	composePath := filepath.Join(projectDir, "compose.yaml")
	if err := os.WriteFile(composePath, []byte(fmt.Sprintf(`name: %s
services:
  web:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 300"]
`, name)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(projectDir, "secrets", "token.txt")
	if err := os.WriteFile(sourcePath, []byte(fileSecretA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dcraftEnv := func(envValue string) []string {
		return buildEnv(map[string]string{
			"DCRAFT_STATE_DIR":          stateDir,
			"DCRAFT_TEST_UPDATE_SECRET": envValue,
		})
	}
	envA := dcraftEnv(envSecretA)
	envB := dcraftEnv(envSecretB)
	run := func(env []string, timeout time.Duration, args ...string) processResult {
		t.Helper()
		res := runDcraftProcess(t, projectDir, env, timeout, args...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stdout", res.stdout, sentinels...)
		assertNoSentinelIn(t, "dcraft "+strings.Join(args, " ")+" stderr", res.stderr, sentinels...)
		return res
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		down := composeCommand(ctx, env, projectDir, name, []string{composePath}, "down", "-v", "--remove-orphans")
		if out, err := down.CombinedOutput(); err != nil {
			t.Logf("cleanup down for %s failed: %v\n%s", name, err, out)
		}
	})

	addEnv := run(envA, 30*time.Second, "secrets", "--add", "-s", "web",
		"--delivery", "environment", "--source", "env", "--ref", "DCRAFT_TEST_UPDATE_SECRET",
		"--target", "APP_SECRET", "-p", name, "--json")
	if addEnv.code != 0 {
		t.Fatalf("add env exit = %d\n%s\n%s", addEnv.code, addEnv.stdout, addEnv.stderr)
	}
	addFile := run(envA, 30*time.Second, "secrets", "--add", "-s", "web",
		"--delivery", "file", "--source", "file", "--ref", "secrets/token.txt",
		"--target", "app_token", "-p", name, "--json")
	if addFile.code != 0 {
		t.Fatalf("add file exit = %d\n%s\n%s", addFile.code, addFile.stdout, addFile.stderr)
	}
	overridePath := parseSecrets(t, addFile.stdout).Data.Secrets.OverrideFile
	if overridePath == "" {
		t.Fatal("managed override path missing")
	}

	up := run(envA, 180*time.Second, "up", "-p", name, "--json")
	if up.code != 0 {
		t.Fatalf("up exit = %d\n%s\n%s", up.code, up.stdout, up.stderr)
	}
	ids := containerIDs(t, env, projectDir, name, []string{composePath})
	if len(ids) != 1 {
		t.Fatalf("containers = %v, want 1", ids)
	}
	first := ids[0]
	if got := dockerExec(t, env, first, `printf %s "$APP_SECRET"`); got != envSecretA {
		t.Fatalf("environment secret before update = %q", got)
	}
	if got := dockerExec(t, env, first, `cat /run/secrets/app_token`); got != fileSecretA {
		t.Fatalf("file secret before update = %q", got)
	}

	// Change both sources, then update. --force-recreate is the explicit path
	// that must deliver the current values.
	if err := os.WriteFile(sourcePath, []byte(fileSecretB+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	update := run(envB, 300*time.Second, "update", "-p", name, "--force-recreate", "--no-pull", "--json")
	if update.code != 0 {
		t.Fatalf("update exit = %d\n%s\n%s", update.code, update.stdout, update.stderr)
	}
	ids = containerIDs(t, env, projectDir, name, []string{composePath})
	if len(ids) != 1 {
		t.Fatalf("containers after update = %v, want 1", ids)
	}
	second := ids[0]
	if second == first {
		t.Fatalf("force-recreate did not replace container %s", first)
	}
	if got := dockerExec(t, env, second, `printf %s "$APP_SECRET"`); got != envSecretB {
		t.Fatalf("environment secret after update = %q, want the changed value", got)
	}
	if got := dockerExec(t, env, second, `cat /run/secrets/app_token`); got != fileSecretB {
		t.Fatalf("file secret after update = %q, want the changed source", got)
	}

	// The record keeps references only, including source kind, ref and target.
	statePath := stateFileFor(t, stateDir)
	if statePath == "" {
		t.Fatal("update did not write the previous-good record")
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	assertNoSentinelIn(t, "previous-good record", string(raw), sentinels...)
	var record state.GoodVersion
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("parse record: %v", err)
	}
	if len(record.Target.SecretRefs) != 2 {
		t.Fatalf("recorded secret refs = %+v", record.Target.SecretRefs)
	}
	refByName := map[string]state.SecretReference{}
	for _, ref := range record.Target.SecretRefs {
		refByName[ref.Name] = ref
	}
	envRef, ok := refByName["DCRAFT_TEST_UPDATE_SECRET"]
	if !ok {
		t.Fatalf("environment reference missing: %+v", record.Target.SecretRefs)
	}
	if envRef.Service != "web" || envRef.Source != "env" || envRef.Delivery != "environment" ||
		envRef.Target != "APP_SECRET" {
		t.Fatalf("environment reference = %+v", envRef)
	}
	fileRef, ok := refByName["secrets/token.txt"]
	if !ok {
		t.Fatalf("file reference missing: %+v", record.Target.SecretRefs)
	}
	if fileRef.Service != "web" || fileRef.Source != "file" || fileRef.Delivery != "file" ||
		fileRef.Target != "app_token" {
		t.Fatalf("file reference = %+v", fileRef)
	}
	assertNoSentinelIn(t, "history", historyFor(t, stateDir), sentinels...)
	scanSecretMetadata(t, stateDir, projectDir, sentinels...)

	// down is the safe cleanup point for both the managed override and the
	// materialized file.
	down := run(envB, 120*time.Second, "down", "-p", name, "--json")
	if down.code != 0 {
		t.Fatalf("down exit = %d\n%s\n%s", down.code, down.stdout, down.stderr)
	}
	if files := materializedFiles(t, stateDir); len(files) != 0 {
		t.Fatalf("stack teardown left materialized files: %v", files)
	}
	if _, err := os.Stat(overridePath); !os.IsNotExist(err) {
		t.Fatalf("stack teardown left the managed override: %v", err)
	}
	if ids := containerIDs(t, env, projectDir, name, []string{composePath}); len(ids) != 0 {
		t.Fatalf("stack teardown left containers: %v", ids)
	}
	scanSecretMetadata(t, stateDir, projectDir, sentinels...)
}
