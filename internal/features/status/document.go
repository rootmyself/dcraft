package status

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Document implements core.ViewPresenter. The TUI renders these four blocks in
// the approved order: summary badges, key-values, the service table, then the
// next-action callout. The CLI keeps using Present, so this method introduces no
// wording change for scripts.
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
			{Kind: core.BlockCallout, Callout: core.CalloutInfo, Text: documentCallout(snap)},
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
		{Text: fmt.Sprintf("서비스 %d", s.Services)},
		{Kind: core.BadgeRunning, Text: fmt.Sprintf("실행 중 %d", s.Running)},
	}
	if s.Instances > 0 {
		badges = append(badges, core.Badge{Text: fmt.Sprintf("인스턴스 %d", s.Instances)})
	}
	if s.Unhealthy > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeUnhealthy, Text: fmt.Sprintf("비정상 %d", s.Unhealthy)})
	}
	if s.Unverified > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeStarting, Text: fmt.Sprintf("확인 안 됨 %d", s.Unverified)})
	}
	if s.Stopped > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeStopped, Text: fmt.Sprintf("중지 %d", s.Stopped)})
	}
	if s.NotCreated > 0 {
		badges = append(badges, core.Badge{Kind: core.BadgeNotCreated, Text: fmt.Sprintf("미생성 %d", s.NotCreated)})
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
		pairs = append(pairs, core.Pair{Key: msgPairObserved, Value: at})
	}
	files := res.Target.Files
	value := strings.Join(files, ", ")
	if len(files) == 0 {
		value = msgNone
	}
	pairs = append(pairs, core.Pair{Key: msgPairComposeFile, Value: value})
	name := snap.ProjectName
	if name == "" {
		name = msgUnknownProject
	}
	pairs = append(pairs, core.Pair{Key: msgPairProject, Value: name})
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
				{Text: svc.Name, Detail: svc.Name},
				{Text: stateLabel(svc.State), Badge: stateBadge(svc.State)},
				{Text: fmt.Sprintf("%d", len(svc.Instances))},
				{Text: documentHealth(svc.Health), Badge: healthBadge(svc.Health)},
				{Text: imageText(svc), Detail: imageText(svc)},
				{Text: portText(svc), Detail: portDetail(svc)},
			},
		})
	}
	return table
}

// stateBadge maps the aggregate state onto the semantic badge. Health is
// deliberately not folded in: a running container with a failing healthcheck is
// still running, and the health column says so.
func stateBadge(state stack.ServiceState) core.BadgeKind {
	switch state {
	case stack.StateRunning:
		return core.BadgeRunning
	case stack.StateExited, stack.StatePaused:
		return core.BadgeStopped
	case stack.StateRestarting:
		return core.BadgeStarting
	case stack.StateNotCreated, stack.StateUnknown, "":
		return core.BadgeNotCreated
	default:
		return core.BadgeNotCreated
	}
}

// documentHealth is the TUI-only Korean wording from the approved decision:
// 정상 / 비정상 / 확인 중 / 없음. The CLI keeps its own health sentences.
func documentHealth(health stack.Health) string {
	switch health {
	case stack.HealthHealthy:
		return msgHealthHealthyTUI
	case stack.HealthUnhealthy:
		return msgHealthUnhealthyTUI
	case stack.HealthStarting:
		return msgHealthStartingTUI
	default:
		return msgHealthNoneTUI
	}
}

func healthBadge(health stack.Health) core.BadgeKind {
	switch health {
	case stack.HealthUnhealthy:
		return core.BadgeUnhealthy
	case stack.HealthStarting:
		return core.BadgeStarting
	default:
		return core.BadgeKind("")
	}
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

// documentCallout is the next action. It mirrors the guidance the CLI prints so
// the TUI never claims a different capability.
//
// The callout has no separate title line: the renderer already labels the block
// with its severity word, and compact mode keeps only the first line. Putting
// "다음 행동" first would mean compact showed the heading and no actual advice.
func documentCallout(snap stack.Snapshot) string {
	if snap.Summary.Services == 0 {
		return msgCalloutEmptyStack
	}
	lines := []string{"1. " + msgCalloutRefresh}
	if snap.Summary.Unhealthy > 0 || snap.Summary.Stopped > 0 {
		lines = append(lines, "2. "+msgCalloutLogs)
	}
	if snap.Summary.NotCreated > 0 {
		lines = append(lines, "3. "+msgCalloutStart)
	}
	return strings.Join(lines, "\n")
}
