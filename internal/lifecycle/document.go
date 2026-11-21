package lifecycle

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/stack/present"
)

// Document implements core.ViewPresenter for the four stack lifecycle features.
// The CLI keeps using Present, so this introduces no wording change there.
//
// Block order follows the approved status screen: a one-line outcome badge, the
// identity and effect key-values, the observed stack table, then the next
// actions. A lifecycle result is a report of what just happened, so the first
// badge states the outcome rather than a count.
func Document(res core.Result) core.Document {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return core.Document{
			Title:  msgTitle,
			Blocks: []core.Block{{Kind: core.BlockParagraph, Text: msgNoPayload}},
		}
	}
	report := payload.Lifecycle
	action := Action(report.Action)

	var blocks []core.Block
	blocks = append(blocks, core.Block{Kind: core.BlockBadges, Badges: documentBadges(action, report)})
	blocks = append(blocks, core.Block{Kind: core.BlockKeyValues, Pairs: documentPairs(res, report, action)})
	if report.State != nil {
		blocks = append(blocks, core.Block{Kind: core.BlockTable, Table: StateTable(*report.State)})
	} else {
		// An unread state is a fact, not an empty stack: say so in a callout
		// instead of rendering a table that would look like "no services".
		blocks = append(blocks, core.Block{
			Kind:    core.BlockCallout,
			Callout: core.CalloutWarn,
			Text:    msgObserveFailed,
		})
	}
	blocks = append(blocks, core.Block{
		Kind:    core.BlockCallout,
		Callout: core.CalloutInfo,
		Text:    strings.Join(numbered(nextActions(action, report.State == nil)), "\n"),
	})
	return core.Document{Title: msgTitle, Blocks: blocks}
}

func documentBadges(action Action, report Report) []core.Badge {
	badges := []core.Badge{{Kind: core.BadgeRunning, Text: doneLine(action)}}
	if len(report.Notices) > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeRisk, Text: fmt.Sprintf(msgBadgeNotices, len(report.Notices))})
	}
	return badges
}

func documentPairs(res core.Result, report Report, action Action) []core.Pair {
	pairs := []core.Pair{
		{Key: msgProjectLabel, Value: report.ProjectName + " (" + nameSourceLabel(report.ProjectNameSource) + ")"},
	}
	if res.Target.ProjectDir != "" {
		pairs = append(pairs, core.Pair{Key: msgFolderLabel, Value: res.Target.ProjectDir})
	}
	if report.Scope == "services" && len(report.Services) > 0 {
		pairs = append(pairs, core.Pair{Key: msgScopeLabel, Value: strings.Join(report.Services, ", ") + " (" + msgScopeServices + ")"})
	} else {
		pairs = append(pairs, core.Pair{Key: msgScopeLabel, Value: msgScopeStack})
	}
	pairs = append(pairs, core.Pair{Key: msgEffectLabel, Value: effectLine(action)})
	for _, notice := range report.Notices {
		pairs = append(pairs, core.Pair{Key: msgNoticesLabel, Value: notice})
	}
	return pairs
}

// StateTable is the observed stack after the operation, in the same column order
// as the status screen. It is exported so the status feature and the lifecycle
// features cannot drift apart on their table shape.
func StateTable(snap stack.Snapshot) *core.Table {
	table := &core.Table{
		EmptyText: msgStateEmpty,
		Columns: []core.Column{
			{Header: msgColumnService, Weight: 30, Min: 12, Max: 24},
			{Header: msgColumnState, Fixed: 11},
			{Header: msgColumnInstances, Fixed: 8, Align: core.AlignRight},
			{Header: msgColumnHealth, Fixed: 8, DropPriority: 3},
			{Header: msgColumnImage, Weight: 45, Min: 16, Max: 34, DropPriority: 2},
			{Header: msgColumnPort, Fixed: 13, DropPriority: 1},
		},
	}
	for _, svc := range snap.Services {
		table.Rows = append(table.Rows, core.Row{
			Selectable: svc.Configured,
			Cells: []core.Cell{
				{Text: svc.Name, Detail: svc.Name},
				{Text: present.StateLabel(svc.State), Badge: present.StateBadge(svc.State)},
				{Text: fmt.Sprintf("%d", len(svc.Instances))},
				{Text: present.HealthLabel(svc.Health), Badge: present.HealthBadge(svc.Health)},
				{Text: svc.Image, Detail: svc.Image},
				{Text: portText(svc), Detail: portDetail(svc)},
			},
		})
	}
	return table
}

func portText(svc stack.Service) string {
	var parts []string
	for _, c := range svc.Connections {
		if c.TargetPort == 0 || c.PublishedPort == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d→%d", c.PublishedPort, c.TargetPort))
	}
	if len(parts) == 0 {
		return msgNoPort
	}
	return strings.Join(parts, ", ")
}

func portDetail(svc stack.Service) string {
	if len(svc.Connections) == 0 {
		return ""
	}
	var lines []string
	for _, c := range svc.Connections {
		if c.TargetPort == 0 {
			continue
		}
		host := c.HostIP
		if host == "" {
			host = msgAllInterfaces
		}
		published := fmt.Sprintf("%d", c.PublishedPort)
		if c.PublishedPort == 0 {
			published = msgDynamicPort
		}
		line := fmt.Sprintf("%s:%s → %d/%s", host, published, c.TargetPort, c.Protocol)
		if c.Source == stack.SourceConfig {
			line += msgConfiguredPort
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// numbered turns guidance into the numbered list the callout renders.
func numbered(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = fmt.Sprintf("%d. %s", i+1, line)
	}
	return out
}
