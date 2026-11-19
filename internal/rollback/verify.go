package rollback

import (
	"context"
	"fmt"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/readiness"
	"github.com/rootmyself/dcraft/internal/stack"
)

// observe runs the shared bounded readiness implementation. Only the injected
// clock, sleep seam and Korean detail strings are feature-local.
func (c *Controller) observe(ctx context.Context, target core.Target, defs []stack.ServiceDefinition, baseline map[string]int, timeout time.Duration) (readiness.Outcome, error) {
	return readiness.Observer{
		Containers: func(ctx context.Context) ([]stack.Container, error) {
			return c.updater.Containers(ctx, target)
		},
		Now:          c.now,
		Sleep:        c.sleep,
		SleepContext: c.sleepContext,
		Poll:         c.poll,
		Settle:       c.settle,
		Messages: readiness.Messages{
			NoContainer:    msgStateNoContainer,
			HealthStarting: msgHealthStarting,
			Restarting:     msgStateRestarting,
			NoState:        msgStateNoContainer,
		},
	}.Observe(ctx, defs, baseline, timeout)
}

// sleepContext is the production sleep seam. Tests set Controller.sleep to
// advance an injected clock instead.
func (c *Controller) sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

func readinessFailure(outcome readiness.Outcome, timeout time.Duration) error {
	summary := readiness.Summary(outcome.Services)
	message := fmt.Sprintf(msgReadinessFailed, summary)
	if outcome.TimedOut {
		message = fmt.Sprintf(msgReadinessTimeout, int(timeout/time.Second), summary)
	}
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryReadinessFailed,
		Severity:    core.SeverityError,
		Message:     message,
		Detail:      readiness.InstanceSummary(outcome.Services, msgStateNoContainer, true),
		NextActions: []string{msgReadinessAct1, msgReadinessAct2, msgReadinessAct3},
	})
}
