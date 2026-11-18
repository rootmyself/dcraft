package hooks

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// Present implements core.Presenter for the hooks command.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return msgNoService
	}
	report := payload.Hooks
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n", msgPresentConfig, report.ConfigFile)
	fmt.Fprintf(&b, "%s: %s\n", msgPresentAction, actionLabel(report.Action))
	if len(report.Registrations) == 0 {
		fmt.Fprintf(&b, "%s\n", msgNoHooks)
	} else {
		b.WriteString(msgPresentList)
		b.WriteString("\n")
		for _, view := range report.Registrations {
			fmt.Fprintf(&b, "  - %s: %s\n", view.Stage, view.Path)
			if view.EffectivePath != "" {
				fmt.Fprintf(&b, "    %s: %s\n", msgPresentEffective, view.EffectivePath)
			}
			if view.SHA256 != "" {
				fmt.Fprintf(&b, "    %s: sha256:%s\n", msgPresentContent, view.SHA256)
			}
			fmt.Fprintf(&b, "    %s: %s\n", msgPresentTrust, trustLabel(view))
			if view.Problem != "" {
				fmt.Fprintf(&b, "    %s: %s\n", msgPresentProblem, view.Problem)
			}
		}
	}
	if len(report.Notices) > 0 {
		b.WriteString("\n")
		for _, notice := range report.Notices {
			fmt.Fprintf(&b, "- %s\n", notice)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func trustLabel(view RegistrationView) string {
	switch {
	case view.Trusted:
		return msgTrustTrusted
	case view.Changed:
		return msgTrustChanged
	case !view.Present:
		return msgTrustUnavailable
	default:
		return msgTrustNeeded
	}
}

func payloadFrom(data any) (Payload, bool) {
	switch v := data.(type) {
	case Payload:
		return v, true
	case *Payload:
		if v != nil {
			return *v, true
		}
	}
	return Payload{}, false
}
