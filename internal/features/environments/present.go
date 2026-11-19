package environments

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
)

// Present implements core.Presenter. It renders alias configuration and Korean
// guidance only; env-file values never reach this text.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := payloadFromResult(res.Data)
	if !ok {
		return msgPresentEmpty
	}
	report := payload.Environments
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", msgPresentTitle)
	switch report.Action {
	case "add":
		fmt.Fprintf(&b, "%s\n", msgPresentAdded)
	case "remove":
		fmt.Fprintf(&b, "%s\n", msgPresentRemoved)
	}
	fmt.Fprintf(&b, "%s: %s\n", msgPresentConfig, report.ConfigFile)
	if len(report.Aliases) == 0 {
		fmt.Fprintf(&b, "\n%s\n", msgPresentEmpty)
	} else {
		b.WriteString("\n")
		b.WriteString(msgPresentListTitle)
		b.WriteString("\n")
		for _, alias := range report.Aliases {
			fmt.Fprintf(&b, "- %s\n", alias.Name)
			fmt.Fprintf(&b, "  %s: %s\n", msgPresentFiles, listText(alias.Files))
			fmt.Fprintf(&b, "  %s: %s\n", msgPresentEnvFiles, listText(alias.EnvFiles))
			fmt.Fprintf(&b, "  %s: %s\n", msgPresentProfiles, listText(alias.Profiles))
		}
	}
	fmt.Fprintf(&b, "\n%s\n", msgPresentLimits)
	fmt.Fprintf(&b, "%s\n", msgPresentAddHint)
	fmt.Fprintf(&b, "%s\n", msgPresentTUIHint)
	_ = width
	return strings.TrimRight(b.String(), "\n")
}

func listText(values []string) string {
	if len(values) == 0 {
		return msgPresentNone
	}
	return strings.Join(values, ", ")
}

func payloadFromResult(data any) (Payload, bool) {
	switch value := data.(type) {
	case Payload:
		return value, true
	case *Payload:
		if value != nil {
			return *value, true
		}
	}
	return Payload{}, false
}
