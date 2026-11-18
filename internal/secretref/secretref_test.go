package secretref

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func writeConfig(t *testing.T, dir, text string) string {
	t.Helper()
	path := filepath.Join(dir, ".dcraft.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadReadsRuntimeSecretReferences(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `environments:
  dev:
    compose_files: [compose.yaml]
secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: DB_PASSWORD
  - service: worker
    phase: runtime
    delivery: file
    source: file
    ref: secrets/token.txt
`)

	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.References) != 2 {
		t.Fatalf("references = %d, want 2", len(cat.References))
	}
	web := cat.References[0]
	if web.Service != "web" || web.Phase != PhaseRuntime || web.Delivery != DeliveryEnvironment ||
		web.Source != SourceEnv || web.Ref != "DB_PASSWORD" || web.Target != "DB_PASSWORD" {
		t.Fatalf("web reference = %+v", web)
	}
	worker := cat.References[1]
	if worker.Target != "token" {
		t.Fatalf("worker target = %q, want token (derived from file name)", worker.Target)
	}
	if worker.TargetPath() != "/run/secrets/token" {
		t.Fatalf("worker target path = %q", worker.TargetPath())
	}

	// The environments section is untouched by loading secrets.
	if _, err := os.Stat(filepath.Join(dir, ".dcraft.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsInvalidReferences(t *testing.T) {
	cases := []struct {
		name string
		text string
		want core.Category
		msg  string
	}{
		{
			name: "unknown field",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: X\n    value: leaked\n",
			want: core.CategorySecretInvalid,
			msg:  "알 수 없는",
		},
		{
			name: "missing source",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    ref: X\n",
			want: core.CategorySecretInvalid,
			msg:  "source",
		},
		{
			name: "duplicate target",
			text: "secrets:\n" +
				"  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: A\n    target: SAME\n" +
				"  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: B\n    target: SAME\n",
			want: core.CategorySecretInvalid,
			msg:  "중복",
		},
		{
			name: "build phase only accepts the buildkit mount",
			text: "secrets:\n  - service: web\n    phase: build\n    delivery: environment\n    source: env\n    ref: X\n",
			want: core.CategorySecretInvalid,
			msg:  "BuildKit",
		},
		{
			name: "aws source is recognized but not implemented",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: aws\n    ref: my-secret\n",
			want: core.CategorySecretInvalid,
			msg:  "AWS",
		},
		{
			name: "unknown delivery",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: socket\n    source: env\n    ref: X\n",
			want: core.CategorySecretInvalid,
			msg:  "delivery",
		},
		{
			name: "unsafe env reference",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: \"BAD-NAME\"\n",
			want: core.CategorySecretInvalid,
			msg:  "환경변수",
		},
		{
			name: "unsafe file target",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: file\n    source: file\n    ref: token.txt\n    target: ../escape\n",
			want: core.CategorySecretInvalid,
			msg:  "target",
		},
		{
			name: "empty value",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: \"  \"\n",
			want: core.CategorySecretInvalid,
			msg:  "비어",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, tc.text)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			var ce *core.Error
			if !asCoreError(err, &ce) {
				t.Fatalf("error = %v, want core.Error", err)
			}
			diag := ce.Diagnostics[0]
			if diag.Category != tc.want {
				t.Fatalf("category = %q, want %q (%+v)", diag.Category, tc.want, diag)
			}
			if !strings.Contains(diag.Message, tc.msg) {
				t.Fatalf("message %q does not contain %q", diag.Message, tc.msg)
			}
		})
	}
}

func TestAddAndRemovePreserveOtherConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `environments:
  dev:
    compose_files: [compose.yaml]
    profiles: [dev]
`)

	cat, err := Add(dir, Reference{
		Service:  "web",
		Phase:    PhaseRuntime,
		Delivery: DeliveryEnvironment,
		Source:   SourceEnv,
		Ref:      "DB_PASSWORD",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(cat.References) != 1 || cat.References[0].Target != "DB_PASSWORD" {
		t.Fatalf("catalog = %+v", cat)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"environments:", "compose_files:", "profiles:", "secrets:", "DB_PASSWORD"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config after Add missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "value:") {
		t.Fatalf("config persisted a value field:\n%s", text)
	}

	cat, err = Add(dir, Reference{
		Service:  "worker",
		Phase:    PhaseRuntime,
		Delivery: DeliveryFile,
		Source:   SourceFile,
		Ref:      "secrets/token.txt",
	})
	if err != nil {
		t.Fatalf("Add file: %v", err)
	}
	if len(cat.References) != 2 || cat.References[1].Target != "token" {
		t.Fatalf("catalog after second add = %+v", cat)
	}

	if _, err := Remove(dir, "web", DeliveryEnvironment, "DB_PASSWORD"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	cat, err = Load(dir)
	if err != nil {
		t.Fatalf("Load after remove: %v", err)
	}
	if len(cat.References) != 1 || cat.References[0].Service != "worker" {
		t.Fatalf("catalog after remove = %+v", cat)
	}

	data, _ = os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if !strings.Contains(string(data), "environments:") {
		t.Fatalf("environments section lost:\n%s", data)
	}
}

func TestAddRejectsDuplicateAndUnknownInput(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: env
    ref: A
`)
	_, err := Add(dir, Reference{
		Service:  "web",
		Phase:    PhaseRuntime,
		Delivery: DeliveryEnvironment,
		Source:   SourceEnv,
		Ref:      "B",
		Target:   "A",
	})
	if err == nil {
		t.Fatal("duplicate target accepted")
	}
	var ce *core.Error
	if !asCoreError(err, &ce) || ce.Diagnostics[0].Category != core.CategorySecretInputInvalid {
		t.Fatalf("error = %v, want secret_input_invalid", err)
	}
}

func TestLoadAcceptsBuildPhaseFileReferences(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `secrets:
  - service: web
    phase: build
    delivery: file
    source: env
    ref: BUILD_TOKEN
  - service: worker
    phase: build
    delivery: file
    source: file
    ref: secrets/token.txt
`)

	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.References) != 2 {
		t.Fatalf("references = %+v", cat.References)
	}
	web := cat.References[0]
	if web.Phase != PhaseBuild || web.Delivery != DeliveryFile || web.Source != SourceEnv {
		t.Fatalf("web reference = %+v", web)
	}
	if web.Target != "BUILD_TOKEN" || web.TargetPath() != "/run/secrets/BUILD_TOKEN" {
		t.Fatalf("web target = %q path = %q", web.Target, web.TargetPath())
	}
	worker := cat.References[1]
	if worker.Phase != PhaseBuild || worker.Target != "token" {
		t.Fatalf("worker reference = %+v", worker)
	}
	if got := cat.ForPhase(PhaseBuild); len(got) != 2 {
		t.Fatalf("build refs = %+v", got)
	}
	if got := cat.ForPhase(PhaseRuntime); len(got) != 0 {
		t.Fatalf("runtime refs = %+v, want none", got)
	}
	if got := cat.ForPhaseServices(PhaseBuild, []string{"worker"}); len(got) != 1 || got[0].Service != "worker" {
		t.Fatalf("filtered build refs = %+v", got)
	}
}

func TestAddRejectsBuildPhaseEnvironmentDelivery(t *testing.T) {
	dir := t.TempDir()
	_, err := Add(dir, Reference{
		Service:  "web",
		Phase:    PhaseBuild,
		Delivery: DeliveryEnvironment,
		Source:   SourceEnv,
		Ref:      "BUILD_TOKEN",
	})
	if err == nil {
		t.Fatal("Add accepted environment delivery for the build phase")
	}
	var ce *core.Error
	if !asCoreError(err, &ce) || ce.Diagnostics[0].Category != core.CategorySecretInputInvalid {
		t.Fatalf("error = %v, want secret_input_invalid", err)
	}
	if !strings.Contains(ce.Diagnostics[0].Message, "BuildKit") {
		t.Fatalf("message = %q", ce.Diagnostics[0].Message)
	}
}

func TestAddAcceptsBuildPhaseFileReferenceWithoutValues(t *testing.T) {
	dir := t.TempDir()
	cat, err := Add(dir, Reference{
		Service:  "web",
		Phase:    PhaseBuild,
		Delivery: DeliveryFile,
		Source:   SourceFile,
		Ref:      "secrets/token.txt",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(cat.References) != 1 || cat.References[0].Phase != PhaseBuild || cat.References[0].Target != "token" {
		t.Fatalf("catalog = %+v", cat)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "value:") || strings.Contains(string(data), "secret-value") {
		t.Fatalf("config persisted a value:\n%s", data)
	}
}

func TestForServicesFiltersByClosure(t *testing.T) {
	cat := Catalog{References: []Reference{
		{Service: "web"},
		{Service: "db"},
		{Service: "worker"},
	}}
	got := cat.ForServices([]string{"web", "db"})
	if len(got) != 2 || got[0].Service != "web" || got[1].Service != "db" {
		t.Fatalf("filtered = %+v", got)
	}
	got = cat.ForServices(nil)
	if len(got) != 3 {
		t.Fatalf("all = %+v", got)
	}
}

func asCoreError(err error, target **core.Error) bool {
	for err != nil {
		if ce, ok := err.(*core.Error); ok {
			*target = ce
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestValidateServicesRejectsRefsWhenConfigHasNoServices(t *testing.T) {
	cat := Catalog{References: []Reference{{Service: "web", Phase: PhaseRuntime, Delivery: DeliveryEnvironment, Source: SourceEnv, Ref: "X"}}}
	if err := ValidateServices(cat, []string{}); err == nil {
		t.Fatal("a non-nil empty service list accepted a reference")
	}
	if err := ValidateServices(cat, nil); err != nil {
		t.Fatalf("nil service list (unknown, used by tests) = %v", err)
	}
}

func TestAddIsIdempotentOnIdenticalReference(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `environments:
  dev:
    compose_files: [compose.yaml]
`)
	ref := Reference{
		Service: "web", Phase: PhaseRuntime, Delivery: DeliveryEnvironment,
		Source: SourceEnv, Ref: "DB_PASSWORD",
	}
	if _, err := Add(dir, ref); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	cat, err := Add(dir, ref)
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if len(cat.References) != 1 {
		t.Fatalf("re-add duplicated the reference: %+v", cat.References)
	}
	data, _ := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if strings.Count(string(data), "ref: DB_PASSWORD") != 1 {
		t.Fatalf("config contains a duplicate:\n%s", data)
	}
}

func TestAddPreservesUnknownTopLevelKeys(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `# team-shared dcraft config
environments:
  dev:
    compose_files: [compose.yaml] # dev files
future_feature:
  enabled: true
  note: keep-me
`)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(dir, Reference{
		Service: "web", Phase: PhaseRuntime, Delivery: DeliveryEnvironment,
		Source: SourceEnv, Ref: "DB_PASSWORD",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# team-shared dcraft config", "# dev files", "future_feature:", "enabled: true", "note: keep-me", "environments:", "compose_files:"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config lost %q:\n%s", want, text)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("config mode = %o, want the existing 644", info.Mode().Perm())
	}
}

func TestFailedWriteLeavesOldConfigIntact(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, dir, `environments:
  dev:
    compose_files: [compose.yaml]
`)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	_, err = Add(dir, Reference{
		Service: "web", Phase: PhaseRuntime, Delivery: DeliveryEnvironment,
		Source: SourceEnv, Ref: "DB_PASSWORD",
	})
	if err == nil {
		t.Skip("filesystem allowed writing to a read-only directory")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("old config disappeared after a failed write: %v", readErr)
	}
	if string(after) != string(before) {
		t.Fatalf("old config changed after a failed write:\n%s", after)
	}
}
