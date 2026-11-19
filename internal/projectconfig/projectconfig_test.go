package projectconfig_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/environments"
	"github.com/rootmyself/dcraft/internal/hooks"
	"github.com/rootmyself/dcraft/internal/projectconfig"
	"github.com/rootmyself/dcraft/internal/secretref"
)

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestMixedConcurrentAddsDoNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	compose := filepath.Join(dir, "compose.yaml")
	writeFile(t, compose, "services:\n  web:\n    image: busybox:1.36\n", 0o644)
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(dir, "scripts", fmt.Sprintf("hook-%d.sh", i)), "#!/bin/sh\nexit 0\n", 0o755)
	}

	const perKind = 4
	start := make(chan struct{})
	errs := make(chan error, perKind*3)
	var wg sync.WaitGroup
	for i := 0; i < perKind; i++ {
		i := i
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_, err := secretref.Add(dir, secretref.Reference{
				Service: "web", Phase: secretref.PhaseRuntime,
				Delivery: secretref.DeliveryEnvironment, Source: secretref.SourceEnv,
				Ref: fmt.Sprintf("SECRET_%d", i),
			})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := hooks.Add(dir, hooks.Registration{
				Path: fmt.Sprintf("scripts/hook-%d.sh", i), Stage: hooks.StagePre,
			})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			_, err := environments.Add(dir, core.Environment{
				Name:  fmt.Sprintf("env%d", i),
				Files: []string{compose},
			})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent add: %v", err)
		}
	}

	cat, err := secretref.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.References) != perKind {
		t.Fatalf("secret refs = %d, want %d: %+v", len(cat.References), perKind, cat.References)
	}
	hookCat, err := hooks.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hookCat.Registrations) != perKind {
		t.Fatalf("hooks = %d, want %d: %+v", len(hookCat.Registrations), perKind, hookCat.Registrations)
	}
	envCat, err := environments.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(envCat.Environments) != perKind {
		t.Fatalf("environments = %d, want %d: %+v", len(envCat.Environments), perKind, envCat.Environments)
	}

	info, err := os.Stat(filepath.Join(dir, projectconfig.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %o, want 600", info.Mode().Perm())
	}
}

func TestUpdateIsAtomicOnMutationFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, projectconfig.FileName)
	const before = "unknown_future:\n  keep: true\n"
	writeFile(t, path, before, 0o640)

	want := errors.New("refuse this change")
	err := projectconfig.Update(dir, func(root *yaml.Node) error {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "secrets"})
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want the mutation error", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != before {
		t.Fatalf("failed update changed the file:\n%s", data)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".dcraft-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", entry.Name())
		}
	}
}

func TestUpdatePreservesUnknownKeysCommentsAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, projectconfig.FileName)
	const before = `# project comment
unknown_future:
  keep: true
environments:
  dev:
    compose_files: [compose.yaml]
`
	writeFile(t, path, before, 0o640)
	if err := projectconfig.Update(dir, func(root *yaml.Node) error {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hooks"},
			&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"},
		)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# project comment", "unknown_future:", "keep: true", "environments:", "hooks:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("written config missing %q:\n%s", want, text)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}
