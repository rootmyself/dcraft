package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

type catalogFeature struct {
	*testFeature
	services []core.ServiceStatus
	err      error
	calls    int
	got      core.Invocation
}

func (f *catalogFeature) Services(ctx context.Context, in core.Invocation) ([]core.ServiceStatus, error) {
	f.calls++
	f.got = in
	return f.services, f.err
}

func serviceTargetFeature() *testFeature {
	return &testFeature{desc: core.Descriptor{
		Name: "stop", Title: "스택 중지", Summary: "스택을 중지합니다.",
		Usage: "dcraft stop", RecordsHistory: true, SupportsServices: true,
	}}
}

func newServiceCatalogHarness(t *testing.T, catalog *catalogFeature, feature core.Feature) (*core.Engine, *core.Registry) {
	t.Helper()
	reg := core.NewRegistry()
	if catalog != nil {
		if err := reg.Register(catalog); err != nil {
			t.Fatalf("Register catalog: %v", err)
		}
	}
	if feature != nil {
		if err := reg.Register(feature); err != nil {
			t.Fatalf("Register feature: %v", err)
		}
	}
	engine := core.NewEngine(reg, stubResolverWithTarget(), &fakePreflight{report: core.PreflightReport{
		Tools: core.ToolInfo{DockerVersion: "29.10.0"},
	}}, &fakeState{dir: "/state"}, &fakeClock{})
	engine.CWD = func() (string, error) { return "/project", nil }
	return engine, reg
}

func TestEngineServicesReturnsTheStatusInspectCatalog(t *testing.T) {
	catalog := &catalogFeature{
		testFeature: &testFeature{desc: readOnlyDescriptor("status")},
		services: []core.ServiceStatus{
			{Name: "api", State: "running", Health: "healthy", Instances: 1, Configured: true},
			{Name: "web", State: "exited", Health: "none", Instances: 1, Configured: true},
		},
	}
	engine, _ := newServiceCatalogHarness(t, catalog, serviceTargetFeature())

	report := engine.Services(context.Background(), core.Request{
		Feature: "stop",
		Target:  core.TargetRequest{Path: "/project"},
	}, nil)
	if report.Failed() {
		t.Fatalf("services failed: %+v", report.Diagnostics)
	}
	if len(report.Services) != 2 || report.Services[0].Name != "api" || report.Services[1].State != "exited" {
		t.Fatalf("services = %+v", report.Services)
	}
	if report.Services[0].Health != "healthy" || report.Services[0].Instances != 1 || !report.Services[0].Configured {
		t.Fatalf("service facts = %+v", report.Services[0])
	}
	if report.Target.ProjectDir != "/project" {
		t.Fatalf("target = %+v", report.Target)
	}
	if catalog.got.Target.ProjectDir != "/project" {
		t.Fatalf("catalog invocation target = %+v", catalog.got.Target)
	}
	var names []string
	for _, st := range report.Stages {
		names = append(names, st.Name)
	}
	if got := strings.Join(names, ","); got != "resolve_target,preflight,service_catalog" {
		t.Fatalf("stages = %v", names)
	}
}

func TestEngineServicesRequiresACatalogWithoutInventingFacts(t *testing.T) {
	engine, _ := newServiceCatalogHarness(t, nil, serviceTargetFeature())
	report := engine.Services(context.Background(), core.Request{Feature: "stop"}, nil)
	if !report.Failed() {
		t.Fatal("services succeeded without a registered catalog")
	}
	if len(report.Services) != 0 {
		t.Fatalf("services invented without a catalog: %+v", report.Services)
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Category != core.CategoryInternal {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
	if !strings.Contains(report.Diagnostics[0].Message, "서비스를 조회할 수 없습니다") {
		t.Fatalf("message = %q", report.Diagnostics[0].Message)
	}
}

func TestEngineServicesPropagatesCatalogFailure(t *testing.T) {
	catalog := &catalogFeature{
		testFeature: &testFeature{desc: readOnlyDescriptor("status")},
		err: core.Fail(core.Diagnostic{
			Category: core.CategoryEngineUnreachable, Severity: core.SeverityError,
			Message: "Docker Engine에 연결할 수 없습니다.",
		}),
	}
	engine, _ := newServiceCatalogHarness(t, catalog, serviceTargetFeature())
	report := engine.Services(context.Background(), core.Request{Feature: "stop"}, nil)
	if !report.Failed() {
		t.Fatal("catalog failure was hidden")
	}
	if len(report.Diagnostics) != 1 || report.Diagnostics[0].Category != core.CategoryEngineUnreachable {
		t.Fatalf("diagnostics = %+v", report.Diagnostics)
	}
}

func TestEngineServicesRejectsFeaturesWithoutServiceSelection(t *testing.T) {
	catalog := &catalogFeature{testFeature: &testFeature{desc: readOnlyDescriptor("status")}}
	feature := &testFeature{desc: readOnlyDescriptor("status2")}
	engine, _ := newServiceCatalogHarness(t, catalog, feature)
	report := engine.Services(context.Background(), core.Request{Feature: "status2"}, nil)
	if !report.Failed() {
		t.Fatal("services succeeded for a feature without SupportsServices")
	}
	if catalog.calls != 0 {
		t.Fatal("catalog was queried for an unsupported feature")
	}
}

func TestEngineServicesReportsCanceled(t *testing.T) {
	catalog := &catalogFeature{testFeature: &testFeature{desc: readOnlyDescriptor("status")}}
	engine, _ := newServiceCatalogHarness(t, catalog, serviceTargetFeature())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := engine.Services(ctx, core.Request{Feature: "stop"}, nil)
	if report.Outcome != core.OutcomeCanceled {
		t.Fatalf("outcome = %q", report.Outcome)
	}
	if catalog.calls != 0 {
		t.Fatal("catalog ran after cancellation")
	}
}

func TestEngineServicesUsesTheRecordedProjectNameWithoutWriting(t *testing.T) {
	catalog := &catalogFeature{testFeature: &testFeature{desc: readOnlyDescriptor("status")}}
	engine, _ := newServiceCatalogHarness(t, catalog, serviceTargetFeature())
	engine.Identity = &fakeIdentityReader{name: "hello-world"}
	report := engine.Services(context.Background(), core.Request{Feature: "stop"}, nil)
	if report.Failed() {
		t.Fatalf("services failed: %+v", report.Diagnostics)
	}
	if catalog.got.Target.ProjectName != "hello-world" || report.Target.ProjectName != "hello-world" {
		t.Fatalf("project name = %q / %q", catalog.got.Target.ProjectName, report.Target.ProjectName)
	}
}

func stubResolverWithTarget() *fakeResolver {
	return &fakeResolver{target: core.Target{
		ProjectDir: "/project",
		Files:      []string{"/project/compose.yaml"},
		EnvFiles:   []string{},
		Profiles:   []string{},
	}}
}

type panickingCatalogFeature struct {
	*testFeature
}

func (f *panickingCatalogFeature) Services(ctx context.Context, in core.Invocation) ([]core.ServiceStatus, error) {
	panic("catalog exploded")
}

func TestEngineServicesRecoversACatalogPanicAsAFailure(t *testing.T) {
	catalog := &panickingCatalogFeature{testFeature: &testFeature{desc: readOnlyDescriptor("status")}}
	engine, _ := newServiceCatalogHarness(t, nil, nil)
	if err := engine.Registry.Register(catalog); err != nil {
		t.Fatal(err)
	}
	if err := engine.Registry.Register(serviceTargetFeature()); err != nil {
		t.Fatal(err)
	}
	report := engine.Services(context.Background(), core.Request{Feature: "stop"}, nil)
	if !report.Failed() {
		t.Fatal("catalog panic was hidden as success")
	}
	found := false
	for _, d := range report.Diagnostics {
		if d.Category == core.CategoryInternal {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v, want internal_error", report.Diagnostics)
	}
}
