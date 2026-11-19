package readiness

import (
	"context"
	"testing"
	"time"

	"github.com/rootmyself/dcraft/internal/stack"
)

func def(name string, health bool) stack.ServiceDefinition {
	return stack.ServiceDefinition{Name: name, Healthcheck: health}
}

func container(name, state, health string, restarts int) stack.Container {
	return stack.Container{
		ID: "c-" + name, Name: name, Service: name, ImageID: "sha256:" + name,
		State: state, Running: state == "running", RestartCount: restarts,
		Health: health, HasHealth: health != "",
	}
}

func observeWith(t *testing.T, defs []stack.ServiceDefinition, containers []stack.Container, baseline map[string]int, timeout time.Duration) Outcome {
	t.Helper()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	outcome, err := Observer{
		Containers: func(context.Context) ([]stack.Container, error) { return containers, nil },
		Now:        func() time.Time { return now },
		Sleep:      func(d time.Duration) { now = now.Add(d) },
		Poll:       time.Second,
		Settle:     2 * time.Second,
		Messages:   Messages{NoContainer: "none", HealthStarting: "starting", Restarting: "restarting", NoState: "no state"},
	}.Observe(context.Background(), defs, baseline, timeout)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	return outcome
}

func TestSharedClassificationCoversPublishedStates(t *testing.T) {
	cases := []struct {
		name       string
		defs       []stack.ServiceDefinition
		containers []stack.Container
		baseline   map[string]int
		timeout    time.Duration
		want       stack.Readiness
		verified   bool
		timedOut   bool
	}{
		{"healthy", []stack.ServiceDefinition{def("web", true)}, []stack.Container{container("web", "running", "healthy", 0)}, nil, 5 * time.Second, stack.ReadinessHealthy, true, false},
		{"running unverified", []stack.ServiceDefinition{def("web", false)}, []stack.Container{container("web", "running", "", 0)}, nil, 5 * time.Second, stack.ReadinessRunningUnverified, false, false},
		{"unhealthy", []stack.ServiceDefinition{def("web", true)}, []stack.Container{container("web", "running", "unhealthy", 0)}, nil, 5 * time.Second, stack.ReadinessUnhealthy, false, false},
		{"exited", []stack.ServiceDefinition{def("web", false)}, []stack.Container{container("web", "exited", "", 0)}, nil, 5 * time.Second, stack.ReadinessExited, false, false},
		{"crash loop", []stack.ServiceDefinition{def("web", false)}, []stack.Container{container("web", "running", "", 3)}, map[string]int{"c-web": 1}, 5 * time.Second, stack.ReadinessRestarting, false, false},
		{"timeout", []stack.ServiceDefinition{def("web", true)}, []stack.Container{container("web", "running", "starting", 0)}, nil, 3 * time.Second, stack.ReadinessTimeout, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome := observeWith(t, tc.defs, tc.containers, tc.baseline, tc.timeout)
			if len(outcome.Services) != 1 || outcome.Services[0].Status != tc.want {
				t.Fatalf("outcome = %+v, want %s", outcome.Services, tc.want)
			}
			if outcome.Verified != tc.verified || outcome.TimedOut != tc.timedOut {
				t.Fatalf("verified=%v timedOut=%v, want %v/%v", outcome.Verified, outcome.TimedOut, tc.verified, tc.timedOut)
			}
		})
	}
}

func TestSettleWindowDetectsARestartAfterASatisfiedRead(t *testing.T) {
	defs := []stack.ServiceDefinition{def("web", false)}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	calls := 0
	outcome, err := Observer{
		Containers: func(context.Context) ([]stack.Container, error) {
			calls++
			if calls >= 3 {
				return []stack.Container{container("web", "running", "", 1)}, nil
			}
			return []stack.Container{container("web", "running", "", 0)}, nil
		},
		Now:    func() time.Time { return now },
		Sleep:  func(d time.Duration) { now = now.Add(d) },
		Poll:   time.Second,
		Settle: 2 * time.Second,
		Messages: Messages{
			NoContainer: "none", HealthStarting: "starting", Restarting: "restarting", NoState: "no state",
		},
	}.Observe(context.Background(), defs, nil, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Services) != 1 || outcome.Services[0].Status != stack.ReadinessRestarting {
		t.Fatalf("outcome = %+v, want restarting after the settle window", outcome.Services)
	}
}
