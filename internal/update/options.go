package update

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

// Options are the parsed explicit update choices. Each choice is independent:
// cache bypass, base-image refresh, image refresh and forced recreation are not
// combinations hidden behind one flag.
type Options struct {
	NoCache       bool
	BuildPull     bool
	Pull          bool
	ForceRecreate bool
	Timeout       time.Duration
}

func (o Options) withDefaults() Options {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	return o
}

// ParseOptions reads the feature flags from a common invocation. Missing
// choices fall back to the ordinary update behavior: cached builds, refresh of
// image-only service references, no forced recreation.
func ParseOptions(in core.Invocation) (Options, error) {
	opts := Options{
		NoCache:       in.BoolOption("no-cache"),
		BuildPull:     in.BoolOption("build-pull"),
		Pull:          !in.BoolOption("no-pull"),
		ForceRecreate: in.BoolOption("force-recreate"),
		Timeout:       defaultTimeout,
	}
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
			NextActions: []string{msgInvalidTimeoutAction},
		})
	}
	opts.Timeout = time.Duration(seconds) * time.Second
	return opts, nil
}
