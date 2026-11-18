package secrets

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/secretref"
)

// Present implements core.Presenter. It renders only saved references and
// Korean guidance; resolved values never reach this text.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return msgListEmpty
	}
	report := payload.Secrets
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", msgListTitle)
	switch report.Action {
	case "add":
		fmt.Fprintf(&b, "%s\n", msgAdded)
	case "remove":
		fmt.Fprintf(&b, "%s\n", msgRemoved)
	}
	fmt.Fprintf(&b, "%s: %s\n", msgListConfig, report.ConfigFile)
	if report.OverrideFile != "" {
		fmt.Fprintf(&b, "%s: %s\n", msgListOverride, report.OverrideFile)
	}
	if len(report.References) == 0 {
		fmt.Fprintf(&b, "\n%s\n", msgListEmpty)
	} else {
		for _, ref := range report.References {
			b.WriteString("\n")
			fmt.Fprintf(&b, "%s · %s\n", ref.Service, phaseLabel(secretrefPhase(ref.Phase)))
			fmt.Fprintf(&b, "  %s: %s\n", msgPreviewDelivery, deliveryLabel(secretrefDelivery(ref.Delivery), ref.Target))
			fmt.Fprintf(&b, "  %s: %s (%s)\n", msgPreviewSource, sourceLabel(secretrefSource(ref.Source)), ref.Ref)
			fmt.Fprintf(&b, "  %s: %s\n", msgPreviewEffect, ref.Effect)
		}
	}
	if len(report.Notices) > 0 {
		fmt.Fprintf(&b, "\n%s\n", msgListNotice)
		for _, notice := range report.Notices {
			fmt.Fprintf(&b, "  - %s\n", notice)
		}
	}
	fmt.Fprintf(&b, "\n%s\n", msgListAddHint)
	fmt.Fprintf(&b, "%s\n", msgListTUIHint)
	_ = width
	return strings.TrimRight(b.String(), "\n")
}

func secretrefPhase(value string) secretref.Phase       { return secretref.Phase(value) }
func secretrefDelivery(value string) secretref.Delivery { return secretref.Delivery(value) }
func secretrefSource(value string) secretref.SourceType { return secretref.SourceType(value) }

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
