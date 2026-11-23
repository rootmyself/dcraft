package tui

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/features/environments"
	"github.com/rootmyself/dcraft/internal/features/status"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/ui/tui/visual"
)

// The golden matrix is the layout contract made reviewable: every main screen is
// rendered at every approved width×height and committed as text, once with the
// painted theme and once with the colorless one. The two sets differ only in
// escape sequences, which the snapshot deliberately strips: what is committed is
// the frame a reader sees, so a diff shows a layout change instead of an ANSI
// byte change. TestGoldenStructureDoesNotDependOnPaint asserts that stripping the
// color from the painted frame yields the colorless frame byte for byte.
//
// Regenerate with:
//   go test ./internal/ui/tui -run TestGolden -update
// or, from the repository root, `scripts/tui-golden.sh` (add -check to verify
// without writing). CI runs the -check form, so a stale snapshot fails the
// build instead of silently drifting.

var updateGolden = flag.Bool("update", false, "rewrite the TUI golden snapshots under internal/ui/tui/testdata/golden")

const goldenRegenCommand = "go test ./internal/ui/tui -run TestGolden -update"

// goldenSizes is the approved matrix: 40/80/120/200 columns × 18/30/50 rows.
var goldenSizes = [][2]int{
	{40, 18}, {40, 30}, {40, 50},
	{80, 18}, {80, 30}, {80, 50},
	{120, 18}, {120, 30}, {120, 50},
	{200, 18}, {200, 30}, {200, 50},
}

// goldenThemeSet is one committed snapshot set: the painted theme and the
// colorless theme resolved by the same pure function the TUI uses in production.
type goldenThemeSet struct {
	key   string
	theme visual.Theme
}

func goldenThemeSets() []goldenThemeSet {
	painted := visual.Resolve(visual.Inputs{
		ThemeSetting: "dark", Profile: visual.ProfileTrueColor,
		DarkBackground: true, UTF8: true,
	})
	return []goldenThemeSet{
		{key: "color", theme: painted},
		{key: "nocolor", theme: visual.Plain()},
	}
}

// goldenScreen is one entry of the matrix. Adding a screen is one entry here.
type goldenScreen struct {
	name  string
	build func(t *testing.T) *Model
}

func goldenScreens() []goldenScreen {
	return []goldenScreen{
		{name: "menu-status", build: goldenMenuStatus},
		{name: "service-selection", build: goldenServiceSelection},
		{name: "confirm-modal", build: goldenConfirmModal},
		{name: "progress", build: goldenProgress},
		{name: "progress-logs", build: goldenProgressLogs},
		{name: "wizard", build: goldenWizard},
		{name: "wizard-list", build: goldenWizardList},
		{name: "wizard-examples", build: goldenWizardExamples},
		{name: "environment-actions", build: goldenEnvironmentActions},
		{name: "environment-switch", build: goldenEnvironmentSwitch},
		{name: "failure", build: goldenFailure},
		{name: "empty-stack", build: goldenEmptyStack},
		{name: "logs", build: goldenLogs},
	}
}

func goldenWizardExamples(t *testing.T) *Model {
	t.Helper()
	feature := environments.New()
	payload, err := feature.Run(context.Background(), core.Invocation{
		Target:  core.Target{ProjectDir: t.TempDir()},
		Options: map[string]string{"wizard": "steps"},
	})
	if err != nil {
		t.Fatal(err)
	}
	desc := core.Descriptor{Name: "secrets", Title: "실행환경 별칭", Summary: "실행환경 별칭을 만듭니다.", Usage: "dcraft environments", Section: core.SectionConfig, Wizard: true}
	stub := &wizardStub{desc: desc, steps: payload.(core.WizardSpec)}
	m := newGoldenModel(t, replaceFeature(goldenBaseFeatures(), "secrets", stub)...)
	if err := chooseFeatureNamed(t, m, "secrets"); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestGoldenMatrix(t *testing.T) {
	for _, screen := range goldenScreens() {
		screen := screen
		t.Run(screen.name, func(t *testing.T) {
			for _, set := range goldenThemeSets() {
				set := set
				m := screen.build(t)
				var b strings.Builder
				fmt.Fprintf(&b, "# dcraft TUI golden snapshot\n")
				fmt.Fprintf(&b, "# screen: %s\n", screen.name)
				fmt.Fprintf(&b, "# theme: %s\n", set.key)
				fmt.Fprintf(&b, "# regenerate: %s\n", goldenRegenCommand)
				for _, size := range goldenSizes {
					resize(t, m, size[0], size[1])
					m.theme = set.theme
					frame := m.View()
					assertGoldenFrame(t, frame, size[0], size[1])
					// The committed text is the frame a reader sees: the color
					// set is stripped so a snapshot diff shows layout, not ANSI
					// bytes. TestGoldenStructureDoesNotDependOnPaint proves the
					// two sets differ only in paint.
					visible := ansi.Strip(frame)
					fmt.Fprintf(&b, "\n=== %dx%d ===\n", size[0], size[1])
					b.WriteString(strings.Join(trimRightLines(strings.Split(visible, "\n")), "\n"))
					b.WriteString("\n")
				}
				path := filepath.Join("testdata", "golden", set.key, screen.name+".txt")
				compareGolden(t, path, b.String())
			}
		})
	}
}

// TestGoldenStructureDoesNotDependOnPaint is the NO_COLOR proof: the same model
// state rendered with the painted theme and with the colorless theme must have
// identical visible text. If a renderer ever encodes structure through color or
// an attribute that changes layout, this fails before the snapshots do.
func TestGoldenStructureDoesNotDependOnPaint(t *testing.T) {
	painted := goldenThemeSets()[0].theme
	plain := visual.Plain()
	for _, screen := range goldenScreens() {
		screen := screen
		t.Run(screen.name, func(t *testing.T) {
			m := screen.build(t)
			resize(t, m, 120, 30)
			m.theme = painted
			colored := m.View()
			m.theme = plain
			colorless := m.View()

			if !strings.Contains(colored, ";38;2;") {
				t.Fatalf("the painted frame carries no truecolor sequence; the color set would prove nothing")
			}
			if strings.Contains(colorless, "\x1b[") {
				t.Fatalf("the colorless frame carries an escape sequence:\n%q", colorless)
			}
			if got := ansi.Strip(colored); got != colorless {
				t.Fatalf("structure depends on paint:\n--- stripped color ---\n%s\n--- colorless ---\n%s", got, colorless)
			}
		})
	}
}

// assertGoldenFrame checks the invariants every committed frame must hold. It is
// deliberately independent of the snapshot text: a wrong snapshot is still
// caught by compareGolden, but a frame that is the wrong size or that cut a
// Korean syllable in half fails here even when -update is rewriting the files.
func assertGoldenFrame(t *testing.T, frame string, width, height int) {
	t.Helper()
	if !utf8.ValidString(frame) {
		t.Fatalf("frame is not valid UTF-8:\n%q", frame)
	}
	lines := strings.Split(frame, "\n")
	if len(lines) != height {
		t.Fatalf("%dx%d: frame has %d rows:\n%s", width, height, len(lines), frame)
	}
	for i, line := range lines {
		if got := visual.Width(line); got > width {
			t.Fatalf("%dx%d: row %d is %d cells, over the frame: %q", width, height, i, got, line)
		}
	}
	if !strings.Contains(lines[0], "dcraft") {
		t.Fatalf("%dx%d: header row lost the stack identity: %q", width, height, lines[0])
	}
	if !strings.Contains(lines[height-1], "[") {
		t.Fatalf("%dx%d: footer row lost its keycaps: %q", width, height, lines[height-1])
	}
}

func trimRightLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(line, " ")
	}
	return out
}

func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create snapshot dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot %s: %v\nregenerate with: %s", path, err, goldenRegenCommand)
	}
	if string(want) == got {
		return
	}
	t.Fatalf("%s is stale.\n%s\nregenerate with: %s", path, firstDifference(string(want), got), goldenRegenCommand)
}

// firstDifference reports the first differing line so a failing snapshot names
// the regression instead of dumping two long files.
func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
	for i := 0; i < len(wantLines) && i < len(gotLines); i++ {
		if wantLines[i] != gotLines[i] {
			return fmt.Sprintf("first difference at line %d:\n  want: %q\n  got:  %q", i+1, wantLines[i], gotLines[i])
		}
	}
	return fmt.Sprintf("line count differs: want %d, got %d", len(wantLines), len(gotLines))
}

// --- fixtures -------------------------------------------------------------
//
// A fixture builds one deterministic model state. States that a command would
// normally reach (a running stage, a live stream) are set directly: the fixture
// exists to render the same screen the command path renders, without a
// goroutine or a wall clock deciding when the frame is photographed.

const (
	goldenProject = "acme-platform"
	goldenDir     = "/work/acme-platform"
)

func goldenTarget() core.TargetRequest {
	return core.TargetRequest{
		Path:        goldenDir,
		ProjectName: goldenProject,
		Environment: "staging",
		Files: []string{
			goldenDir + "/compose.yaml",
			goldenDir + "/compose.staging.yaml",
		},
		Profiles: []string{"observability"},
	}
}

func goldenFacts() visual.Facts {
	return visual.Facts{
		Project:     goldenProject,
		Environment: "staging",
		Docker:      "29.10.0",
		Compose:     "2.39.1",
		Local:       "로컬",
	}
}

// newGoldenModel builds a model with a fixed target and a fixed theme, runs the
// primary feature once, and seeds the header facts so the frame does not depend
// on what the stub preflight happened to report.
func newGoldenModel(t *testing.T, features ...core.Feature) *Model {
	t.Helper()
	th := visual.Plain()
	reg := core.NewRegistry()
	for _, f := range features {
		if err := reg.Register(f); err != nil {
			t.Fatalf("register %s: %v", f.Descriptor().Name, err)
		}
	}
	engine := core.NewEngine(reg, stubResolver{}, stubPreflight{}, stubState{}, stubClock{})
	engine.CWD = func() (string, error) { return goldenDir, nil }
	engine.NewOperationID = func() string { return "op_golden" }
	m := NewModel(context.Background(), Options{
		Engine: engine, Registry: reg, Target: goldenTarget(),
		// A 1ms tick keeps the command pump moving: the spinner command is part
		// of every run/plan batch, and a real interval would make the fixture
		// wait for the wall clock.
		Theme: &th, SpinnerInterval: time.Millisecond,
	})
	initModel(t, m)
	m.spinnerFrame = 0
	m.facts = goldenFacts()
	return m
}

// goldenDescriptorSpec is one feature of the realistic 18-entry registry. The
// order below is the real registration order, so the sectioned menu and the
// sidebar window render exactly what a real build renders.
type goldenDescriptorSpec struct {
	name        string
	title       string
	summary     string
	section     core.Section
	readOnly    bool
	destructive bool
}

var goldenRegistryOrder = []goldenDescriptorSpec{
	{name: "backup", title: "데이터 백업", summary: "볼륨과 폴더를 백업합니다.", section: core.SectionData},
	{name: "down", title: "스택 제거", summary: "컨테이너와 네트워크를 제거합니다.", section: core.SectionOperate, destructive: true},
	{name: "environments", title: "실행환경 별칭", summary: "이름 붙은 실행환경을 관리합니다.", section: core.SectionConfig},
	{name: "exec", title: "서비스 접속", summary: "실행 중인 서비스에 접속합니다.", section: core.SectionInspect, readOnly: true},
	{name: "generate-git-hook", title: "Git 호스트 스크립트 생성", summary: "Git 훅 스크립트를 만듭니다.", section: core.SectionConfig},
	{name: "hooks", title: "호스트 스크립트", summary: "호스트 스크립트를 등록합니다.", section: core.SectionConfig},
	{name: "install-compose", title: "Compose 설치", summary: "Compose 실행 파일을 설치합니다.", section: core.SectionConfig},
	{name: "logs", title: "로그 보기", summary: "서비스 로그를 따라봅니다.", section: core.SectionInspect, readOnly: true},
	{name: "restart", title: "스택 재시작", summary: "스택을 다시 시작합니다.", section: core.SectionOperate},
	{name: "restore", title: "데이터 복원", summary: "백업에서 데이터를 되돌립니다.", section: core.SectionData, destructive: true},
	{name: "rollback", title: "이전 버전 복원", summary: "마지막 정상 버전으로 되돌립니다.", section: core.SectionData, destructive: true},
	{name: "generate-s3-hook", title: "S3 호스트 스크립트 생성", summary: "S3 훅 스크립트를 만듭니다.", section: core.SectionConfig},
	{name: "secrets", title: "비밀값 연결", summary: "비밀값 참조를 등록합니다.", section: core.SectionConfig},
	{name: "status", title: "스택 상태", summary: "현재 스택과 서비스 상태를 보여줍니다.", section: core.SectionInspect, readOnly: true},
	{name: "stop", title: "스택 중지", summary: "선택한 서비스나 스택을 중지합니다.", section: core.SectionOperate},
	{name: "up", title: "스택 시작", summary: "현재 폴더 스택을 시작합니다.", section: core.SectionOperate},
	{name: "update", title: "갱신", summary: "이미지를 갱신하고 변경을 적용합니다.", section: core.SectionOperate},
	{name: "init", title: "최초 구성", summary: "Compose 파일과 .env를 생성합니다.", section: core.SectionConfig},
}

func goldenDescriptor(spec goldenDescriptorSpec, primary bool) core.Descriptor {
	return core.Descriptor{
		Name: spec.name, Title: spec.title, Summary: spec.summary,
		Usage: "dcraft " + spec.name, Section: spec.section,
		ReadOnly: spec.readOnly, Destructive: spec.destructive, Primary: primary,
	}
}

// goldenBaseFeatures is the full 18-feature registry with stub bodies.
func goldenBaseFeatures() []core.Feature {
	out := make([]core.Feature, 0, len(goldenRegistryOrder))
	for _, spec := range goldenRegistryOrder {
		d := goldenDescriptor(spec, spec.name == "status")
		out = append(out, &stubFeature{desc: d, body: spec.title + " 결과"})
	}
	return out
}

// replaceFeature swaps one entry by name, keeping the registry order (and so the
// menu order) intact.
func replaceFeature(list []core.Feature, name string, f core.Feature) []core.Feature {
	out := append([]core.Feature{}, list...)
	for i, existing := range out {
		if existing.Descriptor().Name == name {
			out[i] = f
			return out
		}
	}
	return append(out, f)
}

func featureNamed(t *testing.T, list []core.Feature, name string) core.Feature {
	t.Helper()
	for _, f := range list {
		if f.Descriptor().Name == name {
			return f
		}
	}
	t.Fatalf("feature %q is not registered", name)
	return nil
}

func goldenStatusDescriptor() core.Descriptor {
	for _, spec := range goldenRegistryOrder {
		if spec.name == "status" {
			return goldenDescriptor(spec, true)
		}
	}
	return core.Descriptor{}
}

// goldenSnapshot is a representative stack: a healthy service, an unhealthy one,
// a locally built image, a published port, a stopped service and a service that
// exists only in another environment.
func goldenSnapshot() stack.Snapshot {
	services := []stack.Service{
		{Name: "api", Configured: true, State: stack.StateRunning, Health: stack.HealthHealthy,
			Image: "ghcr.io/acme/platform-api:2026.11.20", Instances: []stack.Instance{{Name: "acme-platform-api-1", State: "running", Health: stack.HealthHealthy}},
			Connections: []stack.Connection{{Source: stack.SourceRuntime, HostIP: "0.0.0.0", PublishedPort: 8080, TargetPort: 8080, Protocol: "tcp"}}},
		{Name: "worker", Configured: true, State: stack.StateRunning, Health: stack.HealthUnhealthy,
			Image: "ghcr.io/acme/platform-worker:2026.11.20", Instances: []stack.Instance{{Name: "acme-platform-worker-1", State: "running", Health: stack.HealthUnhealthy}}},
		{Name: "web", Configured: true, State: stack.StateRunning, Health: stack.HealthStarting,
			Image: "nginx:1.27-alpine", Instances: []stack.Instance{{Name: "acme-platform-web-1", State: "running", Health: stack.HealthStarting}},
			Connections: []stack.Connection{{Source: stack.SourceRuntime, HostIP: "127.0.0.1", PublishedPort: 3000, TargetPort: 80, Protocol: "tcp"}}},
		{Name: "scheduler", Configured: true, State: stack.StateRestarting, Health: stack.HealthNone,
			Image: "ghcr.io/acme/platform-scheduler:2026.11.20", Instances: []stack.Instance{{Name: "acme-platform-scheduler-1", State: "restarting"}}},
		{Name: "db", Configured: true, State: stack.StateRunning, Health: stack.HealthHealthy,
			Image: "postgres:17", Instances: []stack.Instance{{Name: "acme-platform-db-1", State: "running", Health: stack.HealthHealthy}},
			Connections: []stack.Connection{{Source: stack.SourceConfig, HostIP: "0.0.0.0", PublishedPort: 5432, TargetPort: 5432, Protocol: "tcp"}}},
		{Name: "cache", Configured: true, State: stack.StateExited, Health: stack.HealthNone,
			Image: "redis:7-alpine", Instances: []stack.Instance{{Name: "acme-platform-cache-1", State: "exited", ExitCode: 1}}},
		{Name: "migrate", Configured: true, State: stack.StateNotCreated, Health: stack.HealthNone,
			Image: "ghcr.io/acme/platform-api:2026.11.20", Build: true},
		{Name: "legacy", Configured: false, State: stack.StateRunning, Health: stack.HealthNone,
			Image: "ghcr.io/acme/legacy:1.0", Instances: []stack.Instance{{Name: "other-legacy-1", State: "running"}}},
	}
	summary := stack.Summary{Services: len(services)}
	for _, svc := range services {
		summary.Instances += len(svc.Instances)
		switch svc.State {
		case stack.StateRunning:
			summary.Running++
		case stack.StateExited:
			summary.Stopped++
		case stack.StateNotCreated:
			summary.NotCreated++
		}
		switch svc.Health {
		case stack.HealthHealthy:
			summary.Healthy++
		case stack.HealthUnhealthy:
			summary.Unhealthy++
		case stack.HealthStarting:
			summary.Unverified++
		}
	}
	return stack.Snapshot{
		Backend: "compose", ProjectName: goldenProject, ProjectDir: goldenDir,
		Environment: "staging", Services: services, Summary: summary,
	}
}

func goldenStatusResult(snap stack.Snapshot) core.Result {
	return core.Result{
		SchemaVersion: core.SchemaVersion,
		Action:        "status",
		Outcome:       core.OutcomeSuccess,
		Target: core.Target{
			ProjectDir:  goldenDir,
			Files:       []string{goldenDir + "/compose.yaml", goldenDir + "/compose.staging.yaml"},
			Profiles:    []string{"observability"},
			Environment: "staging",
		},
		FinishedAt: time.Date(2026, 11, 20, 9, 14, 5, 0, time.UTC),
		Data:       status.Payload{Stack: snap},
	}
}

func goldenStatusFeature(snap stack.Snapshot) *documentOnlyFeature {
	return &documentOnlyFeature{
		desc: goldenStatusDescriptor(),
		doc:  status.New(nil).Document(goldenStatusResult(snap)),
	}
}

// goldenMenuStatus is the opening screen: the last status result plus the
// sectioned navigation.
func goldenMenuStatus(t *testing.T) *Model {
	t.Helper()
	features := replaceFeature(goldenBaseFeatures(), "status", goldenStatusFeature(goldenSnapshot()))
	return newGoldenModel(t, features...)
}

func goldenEnvironmentActions(t *testing.T) *Model {
	m := goldenMenuStatus(t)
	m.phase = phaseEnvironmentActions
	return m
}

func goldenEnvironmentSwitch(t *testing.T) *Model {
	m := goldenMenuStatus(t)
	m.phase = phaseSelectingEnvironment
	m.envSwitch = true
	m.envs = []core.Environment{{Name: "staging"}, {Name: "production"}}
	m.envSelected = 1
	return m
}

// goldenServiceSelection pins a scope table with more rows than any approved
// window can show, one checked service and the cursor on another.
func goldenServiceSelection(t *testing.T) *Model {
	t.Helper()
	catalog := &catalogStatus{
		stubFeature: &stubFeature{desc: goldenStatusDescriptor(), body: "상태 결과"},
		services:    goldenServiceCatalog(25),
	}
	stop := &serviceCaptureFeature{desc: serviceDescriptor("stop"), body: "중지 결과"}
	features := replaceFeature(goldenBaseFeatures(), "status", catalog)
	features = replaceFeature(features, "stop", stop)
	m := newGoldenModel(t, features...)
	if err := chooseFeatureNamed(t, m, "stop"); err != nil {
		t.Fatal(err)
	}
	// whole -> svc-01 -> check it -> svc-02: one checked row and a cursor on a
	// different row, so both markers are visible in the snapshot.
	pressDown(m)
	pressSpace(m)
	pressDown(m)
	resize(t, m, 120, 30)
	return m
}

// chooseFeatureNamed is chooseFeature without the fatal-on-wrong-phase check:
// fixtures call it before they know the target size.
func chooseFeatureNamed(t *testing.T, m *Model, name string) error {
	t.Helper()
	for i, f := range m.features {
		if f.Descriptor().Name == name {
			m.selected = i
			nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			*m = *(nm.(*Model))
			feed(t, m, execute(t, cmd))
			return nil
		}
	}
	return fmt.Errorf("feature %q is not in the menu", name)
}

func goldenServiceCatalog(n int) []core.ServiceStatus {
	states := []string{"running", "exited", "restarting", "created"}
	healths := []string{"healthy", "none", "unhealthy", "starting"}
	out := make([]core.ServiceStatus, 0, n)
	for i := 0; i < n; i++ {
		state := states[i%len(states)]
		health := healths[i%len(healths)]
		instances := 1
		if state == "exited" || state == "created" {
			instances = 0
		}
		svc := core.ServiceStatus{
			Name:       fmt.Sprintf("svc-%02d", i+1),
			State:      state,
			Health:     health,
			Instances:  instances,
			Configured: i < n-1,
			Image:      fmt.Sprintf("ghcr.io/acme/platform-svc-%02d:2026.11.20", i+1),
		}
		if i%3 == 0 {
			svc.Ports = fmt.Sprintf("%d→8080", 9000+i)
		}
		out = append(out, svc)
	}
	return out
}

// goldenApprovalPlanner is a plan-then-approve feature: it shows the generic
// confirmation panel with a plan body and one independent approval.
type goldenApprovalPlanner struct {
	*stubFeature
}

func (f *goldenApprovalPlanner) Plan(ctx context.Context, in core.Invocation) (core.Plan, error) {
	return core.Plan{Text: strings.Join([]string{
		"출처: https://github.com/docker/compose/releases",
		"버전: v2.39.1",
		"대상 경로: /Users/me/.docker/cli-plugins/docker-compose",
		"무결성: sha256 확인 후 원자적으로 교체합니다.",
	}, "\n")}, nil
}

func goldenConfirmModal(t *testing.T) *Model {
	t.Helper()
	spec := goldenDescriptorSpec{
		name: "install-compose", title: "Compose 설치",
		summary: "dcraft가 Docker Compose 실행 파일을 설치합니다.",
		section: core.SectionConfig,
	}
	d := goldenDescriptor(spec, false)
	d.Requires = core.Requirements{core.RequirementDockerCLI}
	d.Approval = core.ApprovalInstall
	planner := &goldenApprovalPlanner{stubFeature: &stubFeature{desc: d, body: "설치 결과"}}
	features := replaceFeature(goldenBaseFeatures(), "install-compose", planner)
	m := newGoldenModel(t, features...)
	if err := chooseFeatureNamed(t, m, "install-compose"); err != nil {
		t.Fatal(err)
	}
	if m.phase != phaseConfirm {
		t.Fatalf("phase = %v, want the confirmation panel", m.phase)
	}
	resize(t, m, 120, 30)
	return m
}

// goldenProgress is a mutating run in flight: the target, environment and hook
// stages are done, and the execute stage is running. The rows are fed through
// the model's own stage observer, so the timeline is built exactly as it is
// during a real run.
func goldenProgress(t *testing.T) *Model {
	t.Helper()
	up := featureNamed(t, goldenBaseFeatures(), "up")
	d := up.Descriptor()
	d.Hooks = core.HookUpUpdate
	d.RecordsHistory = true
	up = &stubFeature{desc: d, body: "시작 결과"}
	features := replaceFeature(goldenBaseFeatures(), "up", up)
	m := newGoldenModel(t, features...)
	m.active = up
	m.phase = phaseLoading
	// The primary feature already ran during construction; its stages belong to
	// a different operation and must not appear in this timeline.
	m.stages = nil
	plan := core.PlannedStages(up)
	if len(plan) < 5 {
		t.Fatalf("planned stages = %d, want the full run sequence", len(plan))
	}
	m.setPlan(plan)
	for i := 0; i < 4; i++ {
		m.observeStage(core.Stage{Name: plan[i].Name, Title: plan[i].Title, Status: core.StageSucceeded})
	}
	running := core.Stage{Name: plan[4].Name, Title: plan[4].Title, Status: core.StageStarted}
	m.observeStage(running)
	m.currentStage = &running
	return m
}

// goldenProgressLogs is the split running screen: the stage timeline keeps its
// third and the compose progress lines fill the rest in a tail viewport.
func goldenProgressLogs(t *testing.T) *Model {
	t.Helper()
	m := goldenProgress(t)
	lines := []string{
		"web-1  | Pulling nginx:alpine",
		"web-1  | 9a0f2d3c4b5a Pulling fs layer",
		"web-1  | 9a0f2d3c4b5a Downloading 12.4MB",
		"web-1  | 9a0f2d3c4b5a Extracting 1.2MB",
		"web-1  | 9a0f2d3c4b5a Pull complete",
		"api-1  | Building 42.1s (5/7)",
		"api-1  | 7c1b0e2d4f3a CACHED",
		"api-1  | exporting to image",
		"api-1  | naming to docker.io/library/acme-api",
		"cache-1 | Container cache-1  Creating",
		"cache-1 | Container cache-1  Created",
		"web-1  | Container web-1  Creating",
		"web-1  | Container web-1  Created",
		"api-1  | Container api-1  Creating",
		"api-1  | Container api-1  Created",
		"cache-1 | Container cache-1  Starting",
		"web-1  | Container web-1  Starting",
		"api-1  | Container api-1  Starting",
		"cache-1 | Container cache-1  Started",
		"web-1  | Container web-1  Started",
		"api-1  | Container api-1  Started",
	}
	ch := make(chan string, 1)
	m.progress = &progressState{writer: &liveWriter{ch: ch}, lines: lines}
	m.progressCh = ch
	m.progressFollow = true
	return m
}

// goldenWizard is the first question of the guided flow: a choice step rendered
// as selectable cards.
func goldenWizard(t *testing.T) *Model {
	t.Helper()
	desc := core.Descriptor{
		Name: "secrets", Title: "비밀값 연결", Summary: "비밀값 참조를 등록합니다.",
		Usage: "dcraft secrets", Section: core.SectionConfig, Wizard: true,
	}
	stub := &wizardStub{desc: desc, steps: goldenWizardSpec(), preview: goldenWizardPreviewText()}
	features := replaceFeature(goldenBaseFeatures(), "secrets", stub)
	m := newGoldenModel(t, features...)
	if err := chooseFeatureNamed(t, m, "secrets"); err != nil {
		t.Fatal(err)
	}
	if m.phase != phaseWizardStep {
		t.Fatalf("phase = %v, want the first wizard step", m.phase)
	}
	resize(t, m, 120, 30)
	return m
}

func goldenWizardList(t *testing.T) *Model {
	t.Helper()
	m := goldenWizard(t)
	m.wizardSpec = core.WizardSpec{Title: "설정", Steps: []core.WizardStep{{
		Name: "volume", Title: "볼륨", Prompt: "볼륨 입력", AllowCustom: true, List: true, Optional: true,
		CustomHint: "한 줄에 하나",
	}}}
	m.wizardStep = 0
	m.initWizardStep()
	feed(t, m, []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("cache:/data:ro,Z")},
		tea.KeyMsg{Type: tea.KeyEnter},
	})
	return m
}

func goldenWizardSpec() core.WizardSpec {
	return core.WizardSpec{
		Title: "비밀값 연결 설정",
		Steps: []core.WizardStep{
			{
				Name: "service", Title: "서비스 선택", Prompt: "비밀값을 연결할 서비스를 선택하세요.",
				Options: []core.WizardOption{
					{Value: "api", Label: "api · platform-api"},
					{Value: "worker", Label: "worker · platform-worker"},
					{Value: "web", Label: "web · nginx"},
				},
			},
			{
				Name: "delivery", Title: "전달 방식", Prompt: "실행 시점에 값을 어떻게 전달할까요?", Default: "environment",
				Options: []core.WizardOption{
					{Value: "environment", Label: "환경변수"},
					{Value: "file", Label: "파일"},
				},
			},
			{Name: "ref", Title: "원본 참조", Prompt: "원본 참조를 입력하세요.", AllowCustom: true, CustomHint: "환경변수 이름 또는 파일 경로"},
		},
	}
}

func goldenWizardPreviewText() string {
	return strings.Join([]string{
		"저장할 비밀값 연결",
		"서비스: api",
		"전달: 환경변수 (실행 시점)",
		"효과: dcraft up 시 컨테이너 환경변수로 전달합니다.",
	}, "\n")
}

// goldenFailure is a failed run: the shared Korean failure text, its diagnostics
// and the stage timeline, including the stages that never ran.
func goldenFailure(t *testing.T) *Model {
	t.Helper()
	d := goldenDescriptor(goldenDescriptorSpec{
		name: "up", title: "스택 시작", summary: "현재 폴더 스택을 시작합니다.",
		section: core.SectionOperate,
	}, false)
	d.Hooks = core.HookUpUpdate
	d.RecordsHistory = true
	failing := &stubFeature{desc: d, outcome: core.OutcomeFailure}
	features := replaceFeature(goldenBaseFeatures(), "up", failing)
	m := newGoldenModel(t, features...)

	// The run sequence the engine planned, with the execute stage failed and
	// the rows after it never reached.
	plan := core.PlannedStages(failing)
	m.setPlan(core.PlannedStages(failing))
	m.active = failing
	// The primary feature already ran during construction; its stages belong to
	// a different operation.
	m.stages = nil
	// A failed run reports the stages it reached. The engine sends a terminal
	// snapshot per stage, so the result carries the observed rows, not the
	// untouched plan.
	observed := make([]core.Stage, 0, 5)
	for i := 0; i < 4; i++ {
		st := core.Stage{Name: plan[i].Name, Title: plan[i].Title, Status: core.StageSucceeded}
		m.observeStage(st)
		observed = append(observed, st)
	}
	failed := core.Stage{Name: plan[4].Name, Title: plan[4].Title, Status: core.StageFailed}
	m.observeStage(failed)
	observed = append(observed, failed)
	m.result = &core.Result{
		SchemaVersion: core.SchemaVersion,
		OperationID:   "op_golden_failure",
		Action:        "up",
		Outcome:       core.OutcomeFailure,
		Target: core.Target{
			ProjectDir:  goldenDir,
			Environment: "staging",
			Files:       []string{goldenDir + "/compose.yaml", goldenDir + "/compose.staging.yaml"},
		},
		Stages: observed,
		Diagnostics: []core.Diagnostic{{
			Category: core.CategoryLifecycleFailed, Severity: core.SeverityError,
			Message: "스택을 시작하지 못했습니다.",
			Detail:  "Container acme-platform-api-1 exited with code 1",
			NextActions: []string{
				"dcraft logs -s api로 서비스 로그를 확인하세요.",
				"Docker Desktop이 실행 중인지 확인한 뒤 다시 시도하세요.",
			},
		}},
	}
	m.phase = phaseReady
	return m
}

// goldenEmptyStack is a stack with no services: an explained empty state, not a
// blank panel and not a failure.
func goldenEmptyStack(t *testing.T) *Model {
	t.Helper()
	snap := stack.Snapshot{
		Backend: "compose", ProjectName: goldenProject, ProjectDir: goldenDir,
		Environment: "staging", Summary: stack.Summary{},
	}
	features := replaceFeature(goldenBaseFeatures(), "status", goldenStatusFeature(snap))
	// mark .env so the empty-stack key-values show a resolved environment
	return newGoldenModel(t, features...)
}

// goldenLogs is a live log screen with more output than the window can show, so
// the pinned header, the scrolling band and the position row are all visible.
func goldenLogs(t *testing.T) *Model {
	t.Helper()
	features := goldenBaseFeatures()
	m := newGoldenModel(t, features...)
	logs := featureNamed(t, features, "logs")
	lines := make([]string, 0, 60)
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf(
			"api-1  | 2026-11-20T09:14:%02d.%03dZ INFO  request completed method=GET path=/v1/health status=200 duration=%dms",
			i%60, i*7%1000, 3+i%11))
	}
	m.active = logs
	m.phase = phaseLoading
	m.req = core.Request{Feature: "logs", Target: goldenTarget()}
	ch := make(chan string, 1)
	m.live = &liveState{
		mode:   core.LiveStream,
		writer: &liveWriter{ch: ch, tail: lines},
		ch:     ch,
		lines:  lines,
	}
	m.liveCh = ch
	m.liveFollow = true
	return m
}
