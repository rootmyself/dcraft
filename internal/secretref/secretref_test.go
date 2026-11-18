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
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: X\n    mode: leaked\n",
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
			name: "aws-only field on an env source",
			text: "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: env\n    ref: X\n    field: password\n",
			want: core.CategorySecretInvalid,
			msg:  "aws",
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

func TestLoadReadsAWSReferenceShape(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, `secrets:
  - service: web
    phase: runtime
    delivery: environment
    source: aws
    ref: prod/db
    field: database.password
    region: ap-northeast-2
    profile: ops-admin
    target: DB_PASSWORD
  - service: builder
    phase: build
    delivery: file
    source: aws
    ref: arn:aws:secretsmanager:us-east-1:123456789012:secret:build/token-AbCdEf
    field: token
    target: token
`)

	cat, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cat.References) != 2 {
		t.Fatalf("references = %+v", cat.References)
	}
	web := cat.References[0]
	if web.Source != SourceAWS || web.Ref != "prod/db" || web.Field != "database.password" ||
		web.Region != "ap-northeast-2" || web.Profile != "ops-admin" || web.Target != "DB_PASSWORD" {
		t.Fatalf("web reference = %+v", web)
	}
	build := cat.References[1]
	if build.Source != SourceAWS || build.Phase != PhaseBuild || build.Delivery != DeliveryFile ||
		build.Field != "token" || build.Target != "token" {
		t.Fatalf("build reference = %+v", build)
	}
}

func TestAddAWSPersistsOnlyTheReference(t *testing.T) {
	dir := t.TempDir()
	ref := Reference{
		Service:  "web",
		Phase:    PhaseRuntime,
		Delivery: DeliveryEnvironment,
		Source:   SourceAWS,
		Ref:      "prod/db",
		Field:    "database.password",
		Region:   "ap-northeast-2",
		Profile:  "ops-admin",
		Target:   "DB_PASSWORD",
	}
	cat, err := Add(dir, ref)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if len(cat.References) != 1 {
		t.Fatalf("catalog = %+v", cat)
	}
	again, err := Add(dir, ref)
	if err != nil {
		t.Fatalf("re-Add: %v", err)
	}
	if len(again.References) != 1 {
		t.Fatalf("re-add duplicated the AWS reference: %+v", again.References)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".dcraft.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"source: aws", "ref: prod/db", "field: database.password", "region: ap-northeast-2", "profile: ops-admin"} {
		if !strings.Contains(text, want) {
			t.Fatalf("config missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"value:", "aws_access_key", "aws_secret", "session_token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("config stored credential material or a value (%q):\n%s", forbidden, text)
		}
	}
}

func TestValidateAWSReferenceShape(t *testing.T) {
	valid := func() Reference {
		return Reference{
			Service: "web", Phase: PhaseRuntime, Delivery: DeliveryEnvironment,
			Source: SourceAWS, Ref: "prod/db", Field: "database.password",
			Region: "ap-northeast-2", Profile: "ops-admin", Target: "DB_PASSWORD",
		}
	}
	if err := ValidateReference(valid()); err != nil {
		t.Fatalf("valid AWS reference rejected: %v", err)
	}
	arn := valid()
	arn.Ref = "arn:aws:secretsmanager:us-east-1:123456789012:secret:prod/db-AbCdEf"
	arn.Field = "items.0.token"
	if err := ValidateReference(arn); err != nil {
		t.Fatalf("valid AWS ARN/array field rejected: %v", err)
	}
	bare := valid()
	bare.Field, bare.Region, bare.Profile = "", "", ""
	if err := ValidateReference(bare); err != nil {
		t.Fatalf("AWS reference without field/region/profile rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Reference)
		msg    string
	}{
		{"empty ref", func(r *Reference) { r.Ref = "  " }, "이름"},
		{"space in ref", func(r *Reference) { r.Ref = "prod db" }, "ref"},
		{"leading dash ref", func(r *Reference) { r.Ref = "-prod" }, "ref"},
		{"malformed ARN", func(r *Reference) { r.Ref = "arn:aws:s3:::bucket" }, "ARN"},
		{"empty field segment", func(r *Reference) { r.Field = "database..password" }, "field"},
		{"trailing dot field", func(r *Reference) { r.Field = "database." }, "field"},
		{"space in field", func(r *Reference) { r.Field = "database password" }, "field"},
		{"uppercase region", func(r *Reference) { r.Region = "US-EAST-1" }, "리전"},
		{"not a region", func(r *Reference) { r.Region = "notregion" }, "리전"},
		{"profile with space", func(r *Reference) { r.Profile = "ops admin" }, "profile"},
		{"leading dash profile", func(r *Reference) { r.Profile = "-admin" }, "profile"},
		{"field on env source", func(r *Reference) { r.Source = SourceEnv; r.Ref = "DB_PASSWORD" }, "aws"},
		{"region on file source", func(r *Reference) { r.Source = SourceFile; r.Ref = "token.txt" }, "aws"},
		{"profile on env source", func(r *Reference) { r.Source = SourceEnv; r.Ref = "DB_PASSWORD" }, "aws"},
		{"missing target", func(r *Reference) { r.Target = "" }, "target"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref := valid()
			tc.mutate(&ref)
			err := ValidateReference(ref)
			if err == nil {
				t.Fatalf("ValidateReference accepted %+v", ref)
			}
			var ce *core.Error
			if !asCoreError(err, &ce) {
				t.Fatalf("error = %v, want core.Error", err)
			}
			if ce.Diagnostics[0].Category != core.CategorySecretInputInvalid {
				t.Fatalf("category = %q", ce.Diagnostics[0].Category)
			}
			if !strings.Contains(strings.ToLower(ce.Diagnostics[0].Message), strings.ToLower(tc.msg)) {
				t.Fatalf("message %q does not mention %q", ce.Diagnostics[0].Message, tc.msg)
			}
		})
	}
}

func TestLoadRejectsCredentialLikeFields(t *testing.T) {
	for _, field := range []string{"aws_access_key_id", "aws_secret_access_key", "session_token", "access_key", "value"} {
		t.Run(field, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, "secrets:\n  - service: web\n    phase: runtime\n    delivery: environment\n    source: aws\n    ref: prod/db\n    target: DB_PASSWORD\n    "+field+": leaked\n")
			_, err := Load(dir)
			if err == nil {
				t.Fatalf("Load accepted credential-like field %q", field)
			}
			var ce *core.Error
			if !asCoreError(err, &ce) {
				t.Fatalf("error = %v", err)
			}
			if ce.Diagnostics[0].Category != core.CategorySecretInvalid {
				t.Fatalf("category = %q", ce.Diagnostics[0].Category)
			}
			if !strings.Contains(ce.Diagnostics[0].Message, "자격 증명") && !strings.Contains(ce.Diagnostics[0].Message, "알 수 없는") {
				t.Fatalf("message = %q", ce.Diagnostics[0].Message)
			}
		})
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
