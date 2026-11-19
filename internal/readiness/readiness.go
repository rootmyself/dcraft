// Package readiness owns the single bounded readiness implementation shared by
// explicit update and previous-good rollback. It reads live container state
// until every targeted service is verified healthy or running without an app
// healthcheck, a hard failure appears, or the bound expires.
//
// `up --wait` is deliberately not used as proof: it exits 0 for crash-looping
// and no-healthcheck containers.
package readiness

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/stack"
)

// pending is internal: the service is not failing yet but has not satisfied its
// condition inside the bound. Only values in stack.Readiness leave this package.
const pending stack.Readiness = "pending"

// Messages are the feature-local Korean detail strings. The classification
// and timing are shared; the words belong to the calling feature.
type Messages struct {
	NoContainer    string
	HealthStarting string
	Restarting     string
	NoState        string
}

// Observer is one bounded readiness run. Containers is required; zero Poll,
// Settle or Now values use the production defaults. Sleep is the test seam for
// the clock (it advances an injected clock); when nil, a real timer is used.
type Observer struct {
	Containers   func(context.Context) ([]stack.Container, error)
	Now          func() time.Time
	Sleep        func(time.Duration)
	SleepContext func(context.Context, time.Duration) error
	Poll         time.Duration
	Settle       time.Duration
	Messages     Messages
}

// Outcome is the bounded result. Verified is true only when every service had
// a configured healthcheck that passed; TimedOut distinguishes the bound from
// a hard failure.
type Outcome struct {
	Services []stack.ServiceReadiness
	Verified bool
	TimedOut bool
}

// Observe reads the real runtime state until every targeted service is good, a
// hard failure appears, or the bound expires. It never invents health.
func (o Observer) Observe(ctx context.Context, defs []stack.ServiceDefinition, baseline map[string]int, timeout time.Duration) (Outcome, error) {
	if o.Containers == nil {
		return Outcome{}, fmt.Errorf("readiness observer has no container source")
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	start := now()
	deadline := start.Add(timeout)
	var satisfiedAt time.Time
	var satisfiedCounts map[string]int

	for {
		if err := ctx.Err(); err != nil {
			return Outcome{}, err
		}
		containers, err := o.Containers(ctx)
		if err != nil {
			return Outcome{}, err
		}
		services := o.classify(defs, containers, baseline)
		if hardFailure(services) {
			return Outcome{Services: services}, nil
		}
		if AllSatisfied(services) {
			counts := restartCounts(containers)
			if satisfiedAt.IsZero() {
				satisfiedAt = now()
				satisfiedCounts = counts
			} else if now().Sub(satisfiedAt) >= o.settleInterval() {
				if changed := changedServices(containers, satisfiedCounts); len(changed) > 0 {
					return Outcome{Services: o.markRestarting(services, changed)}, nil
				}
				return Outcome{Services: services, Verified: allVerified(services)}, nil
			}
		} else {
			satisfiedAt = time.Time{}
			satisfiedCounts = nil
		}
		if !now().Before(deadline) {
			return Outcome{Services: o.markTimeout(services), TimedOut: true}, nil
		}
		if err := o.sleepContext(ctx, o.pollInterval()); err != nil {
			return Outcome{}, err
		}
	}
}

func (o Observer) pollInterval() time.Duration {
	if o.Poll > 0 {
		return o.Poll
	}
	return 250 * time.Millisecond
}

func (o Observer) settleInterval() time.Duration {
	if o.Settle > 0 {
		return o.Settle
	}
	return 2 * time.Second
}

func (o Observer) sleepContext(ctx context.Context, d time.Duration) error {
	if o.Sleep != nil {
		o.Sleep(d)
		return ctx.Err()
	}
	if o.SleepContext != nil {
		return o.SleepContext(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (o Observer) classify(defs []stack.ServiceDefinition, containers []stack.Container, baseline map[string]int) []stack.ServiceReadiness {
	byService := map[string][]stack.Container{}
	for _, c := range containers {
		byService[c.Service] = append(byService[c.Service], c)
	}
	out := make([]stack.ServiceReadiness, 0, len(defs))
	for _, def := range defs {
		records := byService[def.Name]
		result := stack.ServiceReadiness{
			Service:               def.Name,
			HealthcheckConfigured: def.Healthcheck,
			Instances:             []stack.ReadinessInstance{},
		}
		if len(records) == 0 {
			result.Status = stack.ReadinessNotCreated
			result.Detail = o.Messages.NoContainer
			out = append(out, result)
			continue
		}
		statuses := make([]stack.Readiness, 0, len(records))
		for _, rec := range records {
			result.Instances = append(result.Instances, stack.ReadinessInstance{
				Name:         rec.Name,
				State:        rec.State,
				Health:       rec.Health,
				ExitCode:     rec.ExitCode,
				RestartCount: rec.RestartCount,
				ImageID:      rec.ImageID,
			})
			statuses = append(statuses, classifyContainer(def, rec, baseline))
		}
		result.Status = aggregateStatus(statuses)
		result.AppVerified = result.Status == stack.ReadinessHealthy
		result.Detail = readinessDetail(records)
		out = append(out, result)
	}
	return out
}

func classifyContainer(def stack.ServiceDefinition, rec stack.Container, baseline map[string]int) stack.Readiness {
	restartsExceeded := false
	if prev, ok := baseline[rec.ID]; ok {
		restartsExceeded = rec.RestartCount > prev
	} else {
		restartsExceeded = rec.RestartCount > 0
	}
	switch {
	case rec.Restarting || strings.EqualFold(rec.State, "restarting") || restartsExceeded:
		return stack.ReadinessRestarting
	case strings.EqualFold(rec.State, "exited"), strings.EqualFold(rec.State, "dead"):
		return stack.ReadinessExited
	case strings.EqualFold(rec.State, "created"):
		return stack.ReadinessNotStarted
	case strings.EqualFold(rec.State, "running") || rec.Running:
		if def.Healthcheck {
			switch strings.ToLower(rec.Health) {
			case "healthy":
				return stack.ReadinessHealthy
			case "unhealthy":
				return stack.ReadinessUnhealthy
			default:
				return pending
			}
		}
		return stack.ReadinessRunningUnverified
	default:
		return pending
	}
}

func aggregateStatus(statuses []stack.Readiness) stack.Readiness {
	priority := []stack.Readiness{
		stack.ReadinessRestarting,
		stack.ReadinessExited,
		stack.ReadinessNotStarted,
		stack.ReadinessUnhealthy,
		stack.ReadinessNotCreated,
		pending,
		stack.ReadinessRunningUnverified,
		stack.ReadinessHealthy,
	}
	for _, want := range priority {
		for _, have := range statuses {
			if have == want {
				return want
			}
		}
	}
	return pending
}

func hardFailure(services []stack.ServiceReadiness) bool {
	for _, svc := range services {
		if svc.Status != pending && !svc.Status.Good() {
			return true
		}
	}
	return false
}

// AllSatisfied reports whether every service is good (healthy or running
// without a healthcheck). An empty selection is never satisfied.
func AllSatisfied(services []stack.ServiceReadiness) bool {
	for _, svc := range services {
		if !svc.Status.Good() {
			return false
		}
	}
	return len(services) > 0
}

func allVerified(services []stack.ServiceReadiness) bool {
	for _, svc := range services {
		if svc.Status != stack.ReadinessHealthy {
			return false
		}
	}
	return len(services) > 0
}

func (o Observer) markTimeout(services []stack.ServiceReadiness) []stack.ServiceReadiness {
	out := make([]stack.ServiceReadiness, len(services))
	for i, svc := range services {
		if svc.Status == pending {
			svc.Status = stack.ReadinessTimeout
			if svc.Detail == "" {
				svc.Detail = o.Messages.HealthStarting
			}
		}
		out[i] = svc
	}
	return out
}

func (o Observer) markRestarting(services []stack.ServiceReadiness, changed map[string]bool) []stack.ServiceReadiness {
	out := make([]stack.ServiceReadiness, len(services))
	for i, svc := range services {
		if changed[svc.Service] {
			svc.Status = stack.ReadinessRestarting
			svc.AppVerified = false
			if svc.Detail == "" {
				svc.Detail = o.Messages.Restarting
			}
		}
		out[i] = svc
	}
	return out
}

func restartCounts(containers []stack.Container) map[string]int {
	counts := make(map[string]int, len(containers))
	for _, c := range containers {
		counts[c.ID] = c.RestartCount
	}
	return counts
}

func changedServices(containers []stack.Container, before map[string]int) map[string]bool {
	changed := map[string]bool{}
	for _, c := range containers {
		if before[c.ID] != c.RestartCount {
			changed[c.Service] = true
		}
	}
	return changed
}

func readinessDetail(records []stack.Container) string {
	var parts []string
	for _, rec := range records {
		line := rec.State
		if rec.HasHealth {
			line += " · health:" + rec.Health
		}
		if rec.RestartCount > 0 {
			line += fmt.Sprintf(" · 재시작 %d회", rec.RestartCount)
		}
		if strings.EqualFold(rec.State, "exited") {
			line += fmt.Sprintf(" · 종료 코드 %d", rec.ExitCode)
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, ", ")
}

// Summary is the machine-value summary used in diagnostics:
// "web=unhealthy, db=timeout".
func Summary(services []stack.ServiceReadiness) string {
	parts := make([]string, 0, len(services))
	for _, svc := range services {
		parts = append(parts, svc.Service+"="+string(svc.Status))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// InstanceSummary describes observed instances for a diagnostic detail.
func InstanceSummary(services []stack.ServiceReadiness, noState string, includeImage bool) string {
	var parts []string
	for _, svc := range services {
		for _, in := range svc.Instances {
			line := svc.Service + "/" + in.Name + "=" + in.State
			if in.Health != "" {
				line += "(" + in.Health + ")"
			}
			if in.RestartCount > 0 {
				line += fmt.Sprintf("[restart %d]", in.RestartCount)
			}
			if strings.EqualFold(in.State, "exited") {
				line += fmt.Sprintf("[exit %d]", in.ExitCode)
			}
			if includeImage && in.ImageID != "" {
				line += " image=" + in.ImageID
			}
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return noState
	}
	return strings.Join(parts, ", ")
}
