package selector

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// snapshot builds the read-only view a feature receives from stack.Backend.
// The order is the order status renders: configured services in Compose order.
func snapshot() stack.Snapshot {
	return stack.Snapshot{
		Services: []stack.Service{
			{
				Name:       "db",
				Configured: true,
				State:      stack.StateRunning,
				Instances: []stack.Instance{
					{Name: "proj-db-1", State: "running"},
				},
			},
			{
				Name:       "job",
				Configured: true,
				State:      stack.StateExited,
				Instances: []stack.Instance{
					{Name: "proj-job-1", State: "exited", ExitCode: 1},
				},
			},
			{
				Name:       "web",
				Configured: true,
				State:      stack.StateRunning,
				Instances: []stack.Instance{
					{Name: "proj-web-1", State: "running"},
					{Name: "proj-web-2", State: "running"},
				},
			},
			{
				Name:       "old",
				Configured: false,
				State:      stack.StateExited,
				Instances:  []stack.Instance{{Name: "proj-old-1", State: "exited"}},
			},
		},
	}
}

func diagOf(t *testing.T, err error) core.Diagnostic {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var ce *core.Error
	if !asCoreError(err, &ce) || len(ce.Diagnostics) == 0 {
		t.Fatalf("error is not a core diagnostic: %v", err)
	}
	return ce.Diagnostics[0]
}

func asCoreError(err error, target **core.Error) bool {
	ce, ok := err.(*core.Error)
	if ok {
		*target = ce
	}
	return ok
}

func TestSelectDefaultsToConfiguredServices(t *testing.T) {
	services, err := Select(snapshot(), nil)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(services) != 3 || services[0].Name != "db" || services[1].Name != "job" || services[2].Name != "web" {
		t.Fatalf("services = %+v", services)
	}
	if len(services[2].Instances) != 2 || services[2].Instances[0].Index != 0 || services[2].Instances[1].Index != 1 {
		t.Fatalf("web instances = %+v", services[2].Instances)
	}
	if !services[2].Instances[0].Running || !services[2].Instances[1].Running {
		t.Fatalf("running flags = %+v", services[2].Instances)
	}
	if services[1].Instances[0].Running {
		t.Fatalf("job instance should be stopped: %+v", services[1].Instances)
	}
}

func TestSelectExplicitOrderAndRepeats(t *testing.T) {
	services, err := Select(snapshot(), []string{"web", "db", "web"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(services) != 2 || services[0].Name != "web" || services[1].Name != "db" {
		t.Fatalf("services = %+v", services)
	}
}

func TestSelectRejectsUnknownServiceWithAvailableList(t *testing.T) {
	_, err := Select(snapshot(), []string{"cache"})
	d := diagOf(t, err)
	if d.Category != core.CategoryServiceUnknown {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "cache") || !strings.Contains(d.Message, "찾을 수 없습니다") {
		t.Fatalf("message = %q", d.Message)
	}
	for _, name := range []string{"db", "web"} {
		if !strings.Contains(d.Detail, name) {
			t.Fatalf("detail missing %q: %q", name, d.Detail)
		}
	}
	if strings.Contains(d.Detail, "old") {
		t.Fatalf("detail listed a service outside the current configuration: %q", d.Detail)
	}
	if len(d.NextActions) == 0 {
		t.Fatal("no Korean next actions")
	}
}

func TestChooseInstanceRejectsOutOfRange(t *testing.T) {
	services, err := Select(snapshot(), []string{"web"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	_, err = ChooseInstance(services[0], 2)
	d := diagOf(t, err)
	if d.Category != core.CategoryInstanceUnknown {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "2") || !strings.Contains(d.Detail, "#0") || !strings.Contains(d.Detail, "#1") {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestChooseInstanceAllowsStoppedInstanceForLogs(t *testing.T) {
	services, _ := Select(snapshot(), []string{"job"})
	sel, err := ChooseInstance(services[0], 0)
	if err != nil {
		t.Fatalf("ChooseInstance: %v", err)
	}
	if sel.Chosen == nil || sel.Chosen.Name != "proj-job-1" || sel.Chosen.Index != 0 || sel.Chosen.Running {
		t.Fatalf("chosen = %+v", sel.Chosen)
	}
}

func TestChooseInstanceWithoutIndexKeepsWholeService(t *testing.T) {
	services, _ := Select(snapshot(), []string{"web"})
	sel, err := ChooseInstance(services[0], -1)
	if err != nil {
		t.Fatalf("ChooseInstance: %v", err)
	}
	if sel.Chosen != nil || len(sel.Instances) != 2 {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestOneRunningRequiresExactlyOneService(t *testing.T) {
	_, err := OneRunning(snapshot(), nil, -1)
	d := diagOf(t, err)
	if d.Category != core.CategorySelectionRequired {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Detail, "web") || !strings.Contains(d.Detail, "db") || !strings.Contains(d.Detail, "job") {
		t.Fatalf("detail = %q", d.Detail)
	}

	_, err = OneRunning(snapshot(), []string{"web", "db"}, -1)
	d = diagOf(t, err)
	if d.Category != core.CategorySelectionRequired || !strings.Contains(d.Message, "web") {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestOneRunningRejectsServiceWithoutRunningInstance(t *testing.T) {
	_, err := OneRunning(snapshot(), []string{"job"}, -1)
	d := diagOf(t, err)
	if d.Category != core.CategoryInstanceStopped {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "job") || !strings.Contains(d.Message, "실행 중이 아닙니다") {
		t.Fatalf("message = %q", d.Message)
	}
	if len(d.NextActions) == 0 {
		t.Fatal("no next actions")
	}
}

func TestOneRunningIsAmbiguousWithoutIndex(t *testing.T) {
	_, err := OneRunning(snapshot(), []string{"web"}, -1)
	d := diagOf(t, err)
	if d.Category != core.CategorySelectionRequired {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "여러 개") {
		t.Fatalf("message = %q", d.Message)
	}
	for _, want := range []string{"#0", "proj-web-1", "#1", "proj-web-2", "running"} {
		if !strings.Contains(d.Detail, want) {
			t.Fatalf("detail missing %q: %q", want, d.Detail)
		}
	}
	for _, action := range d.NextActions {
		if strings.Contains(action, "-i") {
			return
		}
	}
	t.Fatalf("next actions do not show -i: %+v", d.NextActions)
}

func TestOneRunningChoosesSoleRunningInstance(t *testing.T) {
	sel, err := OneRunning(snapshot(), []string{"db"}, -1)
	if err != nil {
		t.Fatalf("OneRunning: %v", err)
	}
	if sel.Chosen == nil || sel.Chosen.Name != "proj-db-1" || sel.Chosen.Index != 0 {
		t.Fatalf("chosen = %+v", sel.Chosen)
	}
}

func TestOneRunningHonorsExplicitIndex(t *testing.T) {
	sel, err := OneRunning(snapshot(), []string{"web"}, 0)
	if err != nil {
		t.Fatalf("OneRunning: %v", err)
	}
	if sel.Chosen == nil || sel.Chosen.Name != "proj-web-1" || sel.Service.Name != "web" {
		t.Fatalf("selection = %+v", sel)
	}
}

func TestOneRunningRejectsExplicitStoppedInstance(t *testing.T) {
	_, err := OneRunning(snapshot(), []string{"job"}, 0)
	d := diagOf(t, err)
	if d.Category != core.CategoryInstanceStopped {
		t.Fatalf("category = %q", d.Category)
	}
	if !strings.Contains(d.Message, "proj-job-1") {
		t.Fatalf("message = %q", d.Message)
	}
}

func TestLogTargetsAllowWholeStackWithoutIndex(t *testing.T) {
	targets, err := LogTargets(snapshot(), nil, -1)
	if err != nil {
		t.Fatalf("LogTargets: %v", err)
	}
	if len(targets.Services) != 3 || targets.Picked != nil {
		t.Fatalf("targets = %+v", targets)
	}
}

func TestLogTargetsRequireOneExplicitServiceForIndex(t *testing.T) {
	for _, requested := range [][]string{nil, {"web", "db"}} {
		_, err := LogTargets(snapshot(), requested, 0)
		d := diagOf(t, err)
		if d.Category != core.CategorySelectionRequired {
			t.Fatalf("requested %v: category = %q", requested, d.Category)
		}
		if !strings.Contains(d.Message, "하나") {
			t.Fatalf("message = %q", d.Message)
		}
	}
}

func TestLogTargetsNarrowOneInstance(t *testing.T) {
	targets, err := LogTargets(snapshot(), []string{"web"}, 1)
	if err != nil {
		t.Fatalf("LogTargets: %v", err)
	}
	if targets.Picked == nil || targets.Picked.Chosen == nil || targets.Picked.Chosen.Name != "proj-web-2" {
		t.Fatalf("picked = %+v", targets.Picked)
	}
}

func TestExitCodesDistinguishSelectionFromOperationFailure(t *testing.T) {
	cases := []struct {
		category core.Category
		want     int
	}{
		{core.CategorySelectionRequired, 2},
		{core.CategoryInstanceUnknown, 2},
		{core.CategoryInstanceStopped, 1},
		{core.CategoryLogsFailed, 1},
		{core.CategoryExecFailed, 1},
	}
	for _, tc := range cases {
		res := core.Result{
			Outcome:     core.OutcomeFailure,
			Diagnostics: []core.Diagnostic{{Category: tc.category}},
		}
		if got := res.ExitCode(); got != tc.want {
			t.Fatalf("%s exit = %d, want %d", tc.category, got, tc.want)
		}
	}
}
