package rollback

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/stack/present"
)

// Document implements core.ViewPresenter for a successful rollback. The CLI
// keeps using Present, so its wording and layout stay exactly as they are.
//
// This exists because the TUI otherwise renders the CLI string verbatim, which
// hands an interactive reader command lines to type instead of the menu entries
// that do the same thing.
func Document(res core.Result) core.Document {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return core.Document{
			Title:  msgSuccessHeader,
			Blocks: []core.Block{{Kind: core.BlockParagraph, Text: msgNoController}},
		}
	}
	report := payload.Rollback

	blocks := []core.Block{
		{Kind: core.BlockBadges, Badges: documentBadges(report)},
		{Kind: core.BlockKeyValues, Pairs: documentPairs(res, report)},
	}
	if len(report.Images) > 0 {
		blocks = append(blocks, core.Block{Kind: core.BlockHeading, Text: msgPlanImages})
		blocks = append(blocks, core.Block{Kind: core.BlockTable, Table: imagesTable(report.Images)})
	}
	if len(report.Readiness) > 0 {
		blocks = append(blocks, core.Block{Kind: core.BlockHeading, Text: msgSuccessReadiness})
		blocks = append(blocks, core.Block{Kind: core.BlockTable, Table: readinessTable(report.Readiness)})
	}
	blocks = append(blocks, core.Block{Kind: core.BlockHeading, Text: msgSuccessRecord})
	blocks = append(blocks, core.Block{Kind: core.BlockParagraph, Text: msgSuccessRecordKept})
	if len(report.Notices) > 0 {
		var lines []string
		for i, notice := range report.Notices {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, notice))
		}
		blocks = append(blocks, core.Block{
			Kind:    core.BlockCallout,
			Callout: core.CalloutWarn,
			Text:    strings.Join(lines, "\n"),
		})
	}
	// The report states what happened; the screen's own keys lead onward.
	return core.Document{Title: msgSuccessHeader, Blocks: blocks}
}

// documentBadges states the outcome in one chip. An unverified rollback says so
// rather than claiming the app is healthy.
func documentBadges(report Report) []core.Badge {
	kind := core.BadgeRunning
	text := msgSuccessHeader
	if !report.Verified {
		kind = core.BadgeStarting
		text = msgSuccessUnverified
	}
	return []core.Badge{{Kind: kind, Text: text}}
}

// documentPairs is the identity of what was applied: which project, folder and
// Compose inputs the recorded version used.
func documentPairs(res core.Result, report Report) []core.Pair {
	pairs := []core.Pair{{Key: msgSuccessProject, Value: report.ProjectName, Kind: core.ValueProject}}
	if res.Target.ProjectDir != "" {
		pairs = append(pairs, core.Pair{Key: msgSuccessFolder, Value: res.Target.ProjectDir, Kind: core.ValuePath})
	}
	if len(report.Files) > 0 {
		pairs = append(pairs, core.Pair{Key: msgSuccessFiles, Value: strings.Join(report.Files, ", "), Kind: core.ValuePath})
	}
	if len(report.EnvFiles) > 0 {
		pairs = append(pairs, core.Pair{Key: msgSuccessEnvFiles, Value: strings.Join(report.EnvFiles, ", "), Kind: core.ValuePath})
	}
	if len(report.Profiles) > 0 {
		pairs = append(pairs, core.Pair{Key: msgSuccessProfiles, Value: strings.Join(report.Profiles, ", ")})
	}
	if report.Environment != "" {
		pairs = append(pairs, core.Pair{Key: msgSuccessEnv, Value: report.Environment})
	}
	if len(report.Services) > 0 {
		pairs = append(pairs, core.Pair{Key: msgSuccessServices, Value: strings.Join(report.Services, ", "), Kind: core.ValueService})
	}
	return pairs
}

// imagesTable is the recorded image per service, so the reader can compare what
// ran before with what the record restored.
func imagesTable(pins []stack.RollbackPin) *core.Table {
	table := &core.Table{
		Columns: []core.Column{
			{Header: msgColumnService, Weight: 30, Min: 12, Max: 24},
			{Header: msgColumnImage, Weight: 70, Min: 16, DropPriority: 2},
			{Header: msgColumnTag, Fixed: 16, DropPriority: 1},
		},
	}
	for _, pin := range pins {
		table.Rows = append(table.Rows, core.Row{
			Cells: []core.Cell{
				{Text: pin.Service, Detail: pin.Service, Kind: core.ValueService},
				{Text: pin.ImageID, Detail: pin.ImageID, Kind: core.ValueImage},
				{Text: pin.RetentionTag, Detail: pin.RetentionTag},
			},
		})
	}
	return table
}

// readinessTable is the runtime result per service. It reuses the shared
// readiness vocabulary so this screen and the update screen cannot disagree.
func readinessTable(readiness []stack.ServiceReadiness) *core.Table {
	table := &core.Table{
		Columns: []core.Column{
			{Header: msgColumnService, Weight: 40, Min: 12, Max: 30},
			{Header: msgColumnStatus, Fixed: 14},
			{Header: msgColumnDetail, Weight: 60, Min: 12, DropPriority: 2},
		},
	}
	for _, svc := range readiness {
		table.Rows = append(table.Rows, core.Row{
			Selectable: true,
			Cells: []core.Cell{
				{Text: svc.Service, Detail: svc.Service},
				{Text: readinessLabel(svc), Badge: present.ReadinessBadge(svc.Status)},
				{Text: readinessDetailText(svc), Detail: readinessDetailText(svc)},
			},
		})
	}
	return table
}
