package rollback

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// readinessPending is internal: the service is not failing yet but has not
// satisfied its condition inside the bound. It mirrors the classification
// update publishes; only the values in stack.Readiness ever leave this package.
const readinessPending stack.Readiness = "pending"

type readinessOutcome struct {
	services []stack.ServiceReadiness
	verified bool
	timedOut bool
}

// observe reads the real runtime state until every recorded service is either
// verified healthy or running without an app healthcheck, a hard failure
// appears, or the bound expires. It never uses `up --wait` as proof.
func (c *Controller) observe(ctx context.Context, target core.Target, defs []stack.ServiceDefinition, baseline map[string]int, timeout time.Duration) (readinessOutcome, error) {
	start := c.now()
	deadline := start.Add(timeout)
	var satisfiedAt time.Time
	var satisfiedCounts map[string]int

	for {
		if err := ctx.Err(); err != nil {
			return readinessOutcome{}, err
		}
		containers, err := c.updater.Containers(ctx, target)
		if err != nil {
			return readinessOutcome{}, err
		}
		services := classifyReadiness(defs, containers, baseline)
		if hardFailure(services) {
			return readinessOutcome{services: services}, nil
		}
		if allSatisfied(services) {
			counts := restartCounts(containers)
			if satisfiedAt.IsZero() {
				satisfiedAt = c.now()
				satisfiedCounts = counts
			} else if c.now().Sub(satisfiedAt) >= c.settleInterval() {
				if changed := changedServices(containers, satisfiedCounts); len(changed) > 0 {
					return readinessOutcome{services: markRestarting(services, changed)}, nil
				}
				return readinessOutcome{services: services, verified: allVerified(services)}, nil
			}
		} else {
			satisfiedAt = time.Time{}
			satisfiedCounts = nil
		}
		if !c.now().Before(deadline) {
			return readinessOutcome{services: markTimeout(services), timedOut: true}, nil
		}
		if err := c.sleepContext(ctx, c.pollInterval()); err != nil {
			return readinessOutcome{}, err
		}
	}
}

func (c *Controller) pollInterval() time.Duration {
	if c.poll > 0 {
		return c.poll
	}
	return 250 * time.Millisecond
}

func (c *Controller) settleInterval() time.Duration {
	if c.settle > 0 {
		return c.settle
	}
	return 2 * time.Second
}

func (c *Controller) sleepContext(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		c.sleep(d)
		return ctx.Err()
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

func classifyReadiness(defs []stack.ServiceDefinition, containers []stack.Container, baseline map[string]int) []stack.ServiceReadiness {
	byService := map[string][]stack.Container{}
	for _, container := range containers {
		byService[container.Service] = append(byService[container.Service], container)
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
			result.Detail = msgStateNoContainer
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
				return readinessPending
			}
		}
		return stack.ReadinessRunningUnverified
	default:
		return readinessPending
	}
}

func aggregateStatus(statuses []stack.Readiness) stack.Readiness {
	priority := []stack.Readiness{
		stack.ReadinessRestarting,
		stack.ReadinessExited,
		stack.ReadinessNotStarted,
		stack.ReadinessUnhealthy,
		stack.ReadinessNotCreated,
		readinessPending,
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
	return readinessPending
}

func hardFailure(services []stack.ServiceReadiness) bool {
	for _, svc := range services {
		if svc.Status != readinessPending && !svc.Status.Good() {
			return true
		}
	}
	return false
}

func allSatisfied(services []stack.ServiceReadiness) bool {
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

func markTimeout(services []stack.ServiceReadiness) []stack.ServiceReadiness {
	out := make([]stack.ServiceReadiness, len(services))
	for i, svc := range services {
		if svc.Status == readinessPending {
			svc.Status = stack.ReadinessTimeout
			if svc.Detail == "" {
				svc.Detail = msgHealthStarting
			}
		}
		out[i] = svc
	}
	return out
}

func markRestarting(services []stack.ServiceReadiness, changed map[string]bool) []stack.ServiceReadiness {
	out := make([]stack.ServiceReadiness, len(services))
	for i, svc := range services {
		if changed[svc.Service] {
			svc.Status = stack.ReadinessRestarting
			svc.AppVerified = false
			if svc.Detail == "" {
				svc.Detail = msgStateRestarting
			}
		}
		out[i] = svc
	}
	return out
}

func restartCounts(containers []stack.Container) map[string]int {
	counts := make(map[string]int, len(containers))
	for _, container := range containers {
		counts[container.ID] = container.RestartCount
	}
	return counts
}

func changedServices(containers []stack.Container, before map[string]int) map[string]bool {
	changed := map[string]bool{}
	for _, container := range containers {
		if before[container.ID] != container.RestartCount {
			changed[container.Service] = true
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

// readinessSummary is the machine-value summary used in diagnostics:
// "web=unhealthy, db=timeout".
func readinessSummary(services []stack.ServiceReadiness) string {
	parts := make([]string, 0, len(services))
	for _, svc := range services {
		parts = append(parts, svc.Service+"="+string(svc.Status))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func readinessInstanceSummary(services []stack.ServiceReadiness) string {
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
			if in.ImageID != "" {
				line += " image=" + in.ImageID
			}
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return msgStateNoContainer
	}
	return strings.Join(parts, ", ")
}

// imageMismatches reports every running instance whose image ID is not the
// recorded one. A good readiness classification alone never proves the
// version: only the actual container image does.
func imageMismatches(pins []stack.RollbackPin, services []stack.ServiceReadiness) []string {
	want := make(map[string]string, len(pins))
	for _, pin := range pins {
		want[pin.Service] = pin.ImageID
	}
	var problems []string
	for _, svc := range services {
		wantID, ok := want[svc.Service]
		if !ok {
			continue
		}
		for _, in := range svc.Instances {
			if in.ImageID != wantID {
				problems = append(problems, fmt.Sprintf(msgVerifyImageDetail, svc.Service+"/"+in.Name, wantID, in.ImageID))
			}
		}
	}
	return problems
}

func readinessFailure(outcome readinessOutcome, timeout time.Duration) error {
	summary := readinessSummary(outcome.services)
	message := fmt.Sprintf(msgReadinessFailed, summary)
	if outcome.timedOut {
		message = fmt.Sprintf(msgReadinessTimeout, int(timeout/time.Second), summary)
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryReadinessFailed,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      readinessInstanceSummary(outcome.services),
		NextActions: []string{msgReadinessAct1, msgReadinessAct2, msgReadinessAct3},
	})
}
