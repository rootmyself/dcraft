package rollback

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rootmyself/dcraft/internal/core"
)

// defaultTimeout bounds the readiness observation after apply. It is not a
// command timeout and never makes `up --wait` the source of truth.
const defaultTimeout = 60 * time.Second

const maxTimeout = 3600 * time.Second

// Options are the parsed explicit rollback choices. Rollback has no build,
// pull, cache or forced-recreate choice: it applies exactly the recorded
// image IDs.
type Options struct {
	Timeout time.Duration
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	return o
}

// ParseOptions reads the feature flags from a common invocation.
func ParseOptions(in core.Invocation) (Options, error) {
	opts := Options{Timeout: defaultTimeout}
	raw := strings.TrimSpace(in.Option("timeout"))
	if raw == "" {
		return opts, nil
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds < 1 || time.Duration(seconds)*time.Second > maxTimeout {
		return Options{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInvalidOption,
			Severity:    core.SeverityError,
			Message:     fmt.Sprintf(msgInvalidTimeout, raw),
			Detail:      "timeout",
			NextActions: []string{msgInvalidTimeoutAct},
		})
	}
	opts.Timeout = time.Duration(seconds) * time.Second
	return opts, nil
}
