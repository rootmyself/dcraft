package status

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/stack/present"
)

// Document implements core.ViewPresenter. The TUI renders these three blocks in
// the approved order: summary badges, key-values, and the service table. A
// successful snapshot is a statement of fact; the screen's own keys refresh it,
// so no separate next-action callout repeats them. The CLI keeps using Present.
func (f *Feature) Document(res core.Result) core.Document {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return core.Document{
			Title: msgTitle,
			Blocks: []core.Block{
				{Kind: core.BlockParagraph, Text: msgNoPayload},
			},
		}
	}
	snap := payload.Stack
	return core.Document{
		Title: msgTitle,
		Blocks: []core.Block{
			{Kind: core.BlockBadges, Badges: documentBadges(snap)},
			{Kind: core.BlockKeyValues, Pairs: documentPairs(res, snap)},
			{Kind: core.BlockTable, Table: documentTable(snap)},
		},
	}
}

// documentBadges is the one-glance summary: how many services, how many run,
// how many instances, and how health splits. Zero counts are omitted so the row
// stays readable for a small stack.
func documentBadges(snap stack.Snapshot) []core.Badge {
	s := snap.Summary
	if s.Services == 0 {
		// A row of zeros is noise: one chip states the fact and the table's
		// empty text explains what to check.
		return []core.Badge{{Kind: core.BadgeNotCreated, Text: msgEmptyBadge}}
	}
	// The first and third chips are totals, not states, so they carry no state
	// icon: an icon would claim a meaning the count does not have. Only the
	// state counts below carry a badge kind.
	badges := []core.Badge{
		{Text: fmt.Sprintf(msgBadgeServices, s.Services)},
		{Kind: core.BadgeRunning, Text: fmt.Sprintf(msgBadgeRunning, s.Running)},
	}
	if s.Instances > 0 {
		badges = append(badges, core.Badge{Text: fmt.Sprintf(msgBadgeInstances, s.Instances)})
	}
	if s.Unhealthy > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeUnhealthy, Text: fmt.Sprintf(msgBadgeUnhealthy, s.Unhealthy)})
	}
	if s.Unverified > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeStarting, Text: fmt.Sprintf(msgBadgeUnverified, s.Unverified)})
	}
	if s.Stopped > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeStopped, Text: fmt.Sprintf(msgBadgeStopped, s.Stopped)})
	}
	if s.NotCreated > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeNotCreated, Text: fmt.Sprintf(msgBadgeNotCreated, s.NotCreated)})
	}
	return badges
}

// documentPairs is the last-observed identity: when the read happened, which
// Compose input it used, and which project it belongs to.
func documentPairs(res core.Result, snap stack.Snapshot) []core.Pair {
	// The observation time is omitted, not blank: a zero timestamp is an
	// unknown fact and a dangling label would suggest it was read as empty.
	var pairs []core.Pair
	if at := observedAt(res); at != "" {
		pairs = append(pairs, core.Pair{Key: msgPairObserved, Value: at, Kind: core.ValueTime})
	}
	files := res.Target.Files
	value := strings.Join(files, ", ")
	if len(files) == 0 {
		value = msgNone
	}
	pairs = append(pairs, core.Pair{Key: msgPairComposeFile, Value: value, Kind: core.ValuePath})
	name := snap.ProjectName
	if name == "" {
		name = msgUnknownProject
	}
	pairs = append(pairs, core.Pair{Key: msgPairProject, Value: name, Kind: core.ValueProject})
	return pairs
}

func observedAt(res core.Result) string {
	if res.FinishedAt.IsZero() {
		return ""
	}
	return res.FinishedAt.Format("15:04:05")
}

// documentTable is the comparable view of every service. Columns and widths are
// the approved T1 contract; the renderer owns actual cells and drops columns it
// cannot fit.
func documentTable(snap stack.Snapshot) *core.Table {
	table := &core.Table{
		EmptyText: msgEmptyStack,
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
				{Text: svc.Name, Detail: svc.Name, Kind: core.ValueService},
				{Text: present.StateLabel(svc.State), Badge: present.StateBadge(svc.State)},
				{Text: fmt.Sprintf("%d", len(svc.Instances))},
				{Text: present.HealthLabel(svc.Health), Badge: present.HealthBadge(svc.Health)},
				{Text: imageText(svc), Detail: imageText(svc), Kind: core.ValueImage},
				{Text: portText(svc), Detail: portDetail(svc)},
			},
		})
	}
	return table
}

// imageText is one cell: the image reference, marked when the service builds
// locally. The full reference is the detail value so a truncated cell never
// hides it.
func imageText(svc stack.Service) string {
	switch {
	case svc.Build && svc.Image != "":
		return svc.Image + " " + msgLocalBuild
	case svc.Build:
		return msgLocalBuild
	default:
		return svc.Image
	}
}

// portText joins the published bindings of one service into a single cell; a
// service with no published port renders as the approved dash.
func portText(svc stack.Service) string {
	var parts []string
	for _, c := range svc.Connections {
		if c.Source != stack.SourceRuntime {
			continue
		}
		if c.PublishedPort == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d→%d", c.PublishedPort, c.TargetPort))
	}
	if len(parts) == 0 {
		for _, c := range svc.Connections {
			if c.TargetPort == 0 {
				continue
			}
			parts = append(parts, fmt.Sprintf("%d→%d", c.PublishedPort, c.TargetPort))
		}
	}
	if len(parts) == 0 {
		return msgNoPort
	}
	return strings.Join(parts, ", ")
}

// portDetail is the full binding the compact cell cannot fit: every connection
// with host, protocol and whether it came from the Compose file. Empty when the
// cell already says everything.
func portDetail(svc stack.Service) string {
	if len(svc.Connections) == 0 {
		return ""
	}
	lines := make([]string, 0, len(svc.Connections))
	for _, c := range svc.Connections {
		if c.TargetPort == 0 {
			continue
		}
		lines = append(lines, connectionLabel(c))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}
