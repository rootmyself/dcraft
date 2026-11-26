package initconfig

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// The saved-file document declares the path, the service and the project name.
// The validation command is a fact about what ran; the next command belongs to
// the CLI. The success screen stops at the saved file.
func TestPresentDocumentTagsPathAndCommands(t *testing.T) {
	res := core.Result{Outcome: core.OutcomeSuccess, Data: Payload{Init: Report{
		Mode: "image", Service: "app", Path: "/work/compose.yaml", ProjectName: "work",
		Validation:  Validation{Command: "docker compose config"},
		NextCommand: "dcraft up",
	}}}
	doc := Document(res)
	if len(doc.Blocks) != 1 || doc.Blocks[0].Kind != core.BlockValueLines {
		t.Fatalf("document = %+v", doc)
	}
	kinds := map[core.ValueKind]int{}
	for _, line := range doc.Blocks[0].Lines {
		for _, run := range line {
			kinds[run.Kind]++
		}
	}
	if kinds[core.ValuePath] != 1 || kinds[core.ValueService] != 1 ||
		kinds[core.ValueProject] != 1 || kinds[core.ValueCommand] != 1 {
		t.Fatalf("kinds = %v, want path=1 service=1 project=1 command=1", kinds)
	}
	docText := core.ValueLinesText(doc.Blocks[0].Lines)
	if strings.Contains(docText, msgPreviewNext) {
		t.Fatalf("saved screen still carries a next-action line: %q", docText)
	}
	// The CLI text is unchanged: it still names the command and the folder.
	cliText := Present(res, 0)
	if !strings.Contains(cliText, msgPreviewNext+": "+msgNextCommand) {
		t.Fatalf("CLI text lost its command line: %q", cliText)
	}
}

// The CLI keeps the exact command even when it is not the standard one.
func TestPresentCLIKeepsTheCommandForANonStandardFile(t *testing.T) {
	next := "cd '/work/gen' && dcraft up -f 'stack.yaml'"
	res := core.Result{Outcome: core.OutcomeSuccess, Data: Payload{Init: Report{
		Mode: "image", Service: "app", Path: "/work/gen/stack.yaml", ProjectName: "work",
		NextCommand: next,
	}}}
	cliText := Present(res, 0)
	if !strings.Contains(cliText, next) {
		t.Fatalf("CLI dropped the command: %q", cliText)
	}
	docText := core.ValueLinesText(Document(res).Blocks[0].Lines)
	if strings.Contains(docText, next) {
		t.Fatalf("document repeats a command the success screen should omit: %q", docText)
	}
}
