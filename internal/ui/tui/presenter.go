package tui

import (
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/ui/view"
)

// resolutionKind records which presenter path produced the last result body. It
// exists so the detail view and the tests can state which path was taken without
// re-implementing the resolution rule twice.
type resolutionKind int

const (
	// resolvedDocument means the feature implements core.ViewPresenter.
	resolvedDocument resolutionKind = iota
	// resolvedString means the feature implements only core.Presenter.
	resolvedString
	// resolvedFallback means the feature implements neither; view's generic
	// success text renders in a styled panel.
	resolvedFallback
)

// String is used in test failure messages.
func (k resolutionKind) String() string {
	switch k {
	case resolvedDocument:
		return "document"
	case resolvedString:
		return "string"
	default:
		return "fallback"
	}
}

// resolveResult picks the result body for a successful result. The order is
// fixed: ViewPresenter, then Presenter, then view.SuccessFallback. The CLI keeps
// its own Presenter-only order, so a feature that gains a document cannot change
// CLI output by accident.
func (m *Model) resolveResult(res core.Result) (core.Document, resolutionKind) {
	if m.active != nil {
		if vp, ok := m.active.(core.ViewPresenter); ok {
			return vp.Document(res), resolvedDocument
		}
		if p, ok := m.active.(core.Presenter); ok {
			return stringDocument(p.Present(res, m.bodyWidth())), resolvedString
		}
	}
	return stringDocument(view.SuccessFallback(res)), resolvedFallback
}

// stringDocument wraps a plain presenter string so both paths render through one
// document surface. A string is one paragraph block; its own newlines and blank
// lines are preserved by the paragraph renderer.
func stringDocument(text string) core.Document {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return core.Document{}
	}
	return core.Document{Blocks: []core.Block{{Kind: core.BlockParagraph, Text: text}}}
}

// resultDocument is the document for the current successful result.
func (m *Model) resultDocument() core.Document {
	if m.result == nil || m.result.Outcome != core.OutcomeSuccess {
		return core.Document{}
	}
	doc, _ := m.resolveResult(*m.result)
	return doc
}

// resultMode reports how the current result body is produced.
func (m *Model) resultMode() resolutionKind {
	if m.result == nil {
		return resolvedFallback
	}
	_, kind := m.resolveResult(*m.result)
	return kind
}
