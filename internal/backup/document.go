package backup

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// Document implements core.ViewPresenter for a successful backup. The CLI keeps
// Present, so its full notice list is unchanged.
//
// The interactive screen reports the published archive and then only the
// statements that change a later decision: the backup may hold credentials and
// is not encrypted, a host folder backs up without container ownership, and
// dcraft never removes old backups. How the copy was performed is a fact about
// this run, not a decision, so it is not repeated here.
func Document(res core.Result) core.Document {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return core.Document{
			Title:  msgDoneHeader,
			Blocks: []core.Block{{Kind: core.BlockParagraph, Text: msgNoPayload}},
		}
	}
	report := payload.Backup
	blocks := []core.Block{
		{Kind: core.BlockBadges, Badges: []core.Badge{{Kind: core.BadgeRunning, Text: msgDoneHeader}}},
		{Kind: core.BlockKeyValues, Pairs: documentPairs(report)},
	}
	if len(report.Artifacts) > 0 {
		blocks = append(blocks, core.Block{Kind: core.BlockTable, Table: artifactTable(report.Artifacts)})
	}
	if warnings := documentWarnings(report); len(warnings) > 0 {
		blocks = append(blocks, core.Block{
			Kind:    core.BlockCallout,
			Callout: core.CalloutWarn,
			Text:    strings.Join(warnings, "\n"),
		})
	}
	return core.Document{Title: msgDoneHeader, Blocks: blocks}
}

func documentPairs(report Report) []core.Pair {
	pairs := []core.Pair{
		{Key: msgProjectLabel, Value: report.ProjectName, Kind: core.ValueProject},
		{Key: msgOutputLabel, Value: report.OutputDir, Kind: core.ValuePath},
		{Key: msgSourcesLabel, Value: joinOrNone(sourceKeys(report.Sources))},
		{Key: msgServicesLabel, Value: joinOrNone(report.AffectedServices)},
	}
	if len(report.Stopped) > 0 {
		pairs = append(pairs, core.Pair{Key: msgStoppedLabel, Value: strings.Join(report.Stopped, ", ")})
	}
	if len(report.Restarted) > 0 {
		pairs = append(pairs, core.Pair{Key: msgRestartedLabel, Value: strings.Join(report.Restarted, ", ")})
	}
	return pairs
}

// artifactTable is one row per published archive. The SHA-256 and the data size
// are omitted on the interactive screen: they are verification facts for a
// script, and the completion state is what a reader checks here.
func artifactTable(artifacts []ArtifactReport) *core.Table {
	table := &core.Table{Columns: []core.Column{
		{Header: msgColumnSource, Weight: 26, Min: 12, Max: 22},
		{Header: msgColumnState, Fixed: 8},
		{Header: msgColumnPath, Weight: 45, Min: 16, Max: 40, DropPriority: 1},
		{Header: msgColumnSize, Fixed: 10, Align: core.AlignRight},
		{Header: msgColumnEntries, Fixed: 8, Align: core.AlignRight, DropPriority: 2},
	}}
	for _, art := range artifacts {
		state, badge := msgComplete, core.BadgeRunning
		if !art.Complete {
			state, badge = msgIncomplete, core.BadgeUnhealthy
		}
		table.Rows = append(table.Rows, core.Row{Cells: []core.Cell{
			{Text: artifactSourceLabel(art.Source), Detail: art.Path},
			{Text: state, Badge: badge},
			{Text: art.Path, Detail: art.Path, Kind: core.ValuePath},
			{Text: humanBytes(art.ArtifactSizeBytes)},
			{Text: fmt.Sprintf("%d", art.Entries)},
		}})
	}
	return table
}

// artifactSourceLabel names the archived data the way the source list does.
func artifactSourceLabel(src Source) string {
	if src.Kind == data.KindBind {
		return "폴더 " + src.HostPath
	}
	if src.VolumeKey != "" {
		return "볼륨 " + src.VolumeKey
	}
	return "볼륨 " + src.VolumeName
}

// documentWarnings keeps only the statements a reader must carry forward.
func documentWarnings(report Report) []string {
	out := []string{msgTUIDataRisk}
	if anyBind(report.Sources) {
		out = append(out, msgTUIHostFolderLimit)
	}
	out = append(out, msgTUIKeepOld)
	return out
}

func anyBind(sources []Source) bool {
	for _, src := range sources {
		if src.Kind == data.KindBind {
			return true
		}
	}
	return false
}
