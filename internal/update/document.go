package update

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
	"github.com/rootmyself/dcraft/internal/stack/present"
)

// Document implements core.ViewPresenter for a successful update. The CLI keeps
// using Present, so this introduces no wording change there.
//
// The report is a sequence of facts about one run: what happened (badges and
// key-values), the services that were handled, their readiness, the recorded
// good version, and the next actions. Readiness is a table because the reader
// compares services vertically.
func Document(res core.Result) core.Document {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return core.Document{
			Title:  msgPlanHeader,
			Blocks: []core.Block{{Kind: core.BlockParagraph, Text: msgNoUpdater}},
		}
	}
	report := payload.Update

	blocks := []core.Block{
		{Kind: core.BlockBadges, Badges: documentBadges(report)},
		{Kind: core.BlockKeyValues, Pairs: documentPairs(res, report)},
	}
	if len(report.Readiness) > 0 {
		blocks = append(blocks, core.Block{Kind: core.BlockHeading, Text: msgSuccessReadiness})
		blocks = append(blocks, core.Block{Kind: core.BlockTable, Table: readinessTable(report.Readiness)})
	}
	if len(report.Notices) > 0 {
		blocks = append(blocks, core.Block{Kind: core.BlockHeading, Text: msgNoticesLabel})
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
	blocks = append(blocks, core.Block{Kind: core.BlockHeading, Text: msgSuccessRecord})
	blocks = append(blocks, core.Block{Kind: core.BlockParagraph, Text: recordText(report)})
	blocks = append(blocks, core.Block{
		Kind:    core.BlockCallout,
		Callout: core.CalloutInfo,
		Text: strings.Join([]string{
			"1. " + msgDocNextStatus,
			"2. " + msgDocNextUpdate,
			"3. " + msgSuccessNextRoll,
		}, "\n"),
	})
	return core.Document{Title: msgSuccessHeader, Blocks: blocks}
}

func documentBadges(report Report) []core.Badge {
	kind := core.BadgeRunning
	text := msgSuccessHeader
	if !report.Verified {
		kind = core.BadgeStarting
		text = msgSuccessUnverified
	}
	badges := []core.Badge{{Kind: kind, Text: text}}
	if len(report.Built) > 0 {
		badges = append(badges, core.Badge{Text: fmt.Sprintf("%s %d", msgSuccessBuild, len(report.Built))})
	}
	if len(report.Pulled) > 0 {
		badges = append(badges, core.Badge{Text: fmt.Sprintf("%s %d", msgSuccessPull, len(report.Pulled))})
	}
	return badges
}

func documentPairs(res core.Result, report Report) []core.Pair {
	pairs := []core.Pair{{Key: msgSuccessProject, Value: report.ProjectName, Kind: core.ValueProject}}
	if res.Target.ProjectDir != "" {
		pairs = append(pairs, core.Pair{Key: msgSuccessFolder, Value: res.Target.ProjectDir, Kind: core.ValuePath})
	}
	files := strings.Join(res.Target.Files, ", ")
	if files == "" {
		files = msgSuccessNone
	}
	pairs = append(pairs, core.Pair{Key: msgSuccessFiles, Value: files, Kind: core.ValuePath})
	if res.Target.Environment != "" {
		pairs = append(pairs, core.Pair{Key: msgSuccessEnv, Value: res.Target.Environment})
	}
	scope := msgSuccessWhole
	if report.Scope == "services" {
		scope = strings.Join(report.Services, ", ")
	}
	pairs = append(pairs, core.Pair{Key: msgSuccessServices, Value: scope})
	build := msgSuccessNone
	if len(report.Built) > 0 {
		build = strings.Join(report.Built, ", ")
	}
	pull := msgSuccessNone
	if len(report.Pulled) > 0 {
		pull = strings.Join(report.Pulled, ", ")
	}
	apply := msgPlanDefaultRecreate
	if report.Options.ForceRecreate {
		apply = msgPlanForceRecreate
	}
	pairs = append(pairs,
		core.Pair{Key: msgSuccessBuild, Value: build},
		core.Pair{Key: msgSuccessPull, Value: pull},
		core.Pair{Key: msgSuccessApply, Value: strings.TrimSpace(apply)},
	)
	return pairs
}

// readinessTable is the per-service verification result. The status column is a
// badge so a failure reads without color.
func readinessTable(readiness []stack.ServiceReadiness) *core.Table {
	table := &core.Table{
		EmptyText: msgSuccessNoServices,
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

// recordText states what the good-version recording did, without ever claiming a
// record that was not written.
func recordText(report Report) string {
	switch report.RecordStatus {
	case RecordRecorded:
		lines := goodVersionSummary(report.GoodVersion)
		if report.GoodVersion != nil {
			lines = append(lines, msgGoodRetentionNote)
		}
		return strings.Join(lines, "\n")
	case RecordFailed:
		lines := []string{msgGoodRecordFailed}
		if strings.TrimSpace(report.RecordDetail) != "" {
			lines = append(lines, report.RecordDetail)
		}
		return strings.Join(lines, "\n")
	default:
		return msgSuccessNone
	}
}
