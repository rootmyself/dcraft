package update

import (
	"context"
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
			NoState:        msgReadinessNoState,
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
