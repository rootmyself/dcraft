package restore

import (
	"fmt"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// Document implements core.ViewPresenter for a successful restore. The CLI keeps
// Present, so its full notice and warning lists are unchanged.
//
// The interactive screen reports the replaced data and then only the statements
// that change a later decision: the replacement is not a merge, the safety
// backup holds the previous state, dcraft never deletes old backups, and the
// platform-dependent limits of ownership restore. The service pause and the
// hook behaviour are facts about this run.
func Document(res core.Result) core.Document {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return core.Document{
			Title:  msgDoneHeader,
			Blocks: []core.Block{{Kind: core.BlockParagraph, Text: msgDocNoPayload}},
		}
	}
	report := payload.Restore
	blocks := []core.Block{
		{Kind: core.BlockBadges, Badges: []core.Badge{{Kind: core.BadgeRunning, Text: msgDoneHeader}}},
		{Kind: core.BlockKeyValues, Pairs: documentPairs(report)},
	}
	if len(report.Warnings) > 0 {
		blocks = append(blocks, core.Block{
			Kind:    core.BlockCallout,
			Callout: core.CalloutWarn,
			Text:    numberedList(report.Warnings),
		})
	}
	blocks = append(blocks, core.Block{
		Kind:    core.BlockCallout,
		Callout: core.CalloutWarn,
		Text:    numberedList(documentWarnings(report)),
	})
	return core.Document{Title: msgDoneHeader, Blocks: blocks}
}

func documentPairs(report Report) []core.Pair {
	pairs := []core.Pair{
		{Key: msgDocProject, Value: report.ProjectName, Kind: core.ValueProject},
		{Key: msgDocBackup, Value: report.ArtifactPath, Kind: core.ValuePath},
	}
	if report.TargetKind == data.KindBind {
		pairs = append(pairs, core.Pair{Key: msgDocTarget, Value: report.HostPath, Kind: core.ValuePath})
	} else {
		pairs = append(pairs, core.Pair{Key: msgDocTarget, Value: report.VolumeKey + " → " + report.VolumeName})
	}
	pairs = append(pairs, core.Pair{Key: msgDocDataState, Value: string(report.DataState)})
	if report.SafetyBackup != "" {
		pairs = append(pairs, core.Pair{Key: msgDocSafetyBackup, Value: report.SafetyBackup, Kind: core.ValuePath})
	}
	if len(report.Warnings) > 0 {
		pairs = append(pairs, core.Pair{Key: msgDocWarnings, Value: fmt.Sprintf(msgDocWarningCount, len(report.Warnings))})
	}
	return pairs
}

// documentWarnings keeps only the statements a reader must carry forward.
func documentWarnings(report Report) []string {
	lines := []string{msgTUIReplace, msgTUISafety, msgTUIKeepOld}
	if report.TargetKind == data.KindBind {
		lines = append(lines, msgTUIHostFolderLimit)
	} else {
		lines = append(lines, msgTUIOwnershipLimit)
	}
	return lines
}

func numberedList(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += fmt.Sprintf("%d. %s", i+1, line)
	}
	return out
}
