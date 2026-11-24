package initconfig

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// The saved-file document declares the path, the service, the project name and
// the validation command. A standard file in the project folder is started from
// the menu, so that screen carries no command line; the CLI keeps it.
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
	if !strings.Contains(docText, msgDocNext) {
		t.Fatalf("saved screen lost the menu guidance: %q", docText)
	}
	if strings.Contains(docText, msgNextCommand) {
		t.Fatalf("saved screen still tells an interactive reader to type the command: %q", docText)
	}
	// The CLI text is unchanged: it still names the command and the folder.
	cliText := Present(res, 0)
	if !strings.Contains(cliText, msgPreviewNext+": "+msgNextCommand) {
		t.Fatalf("CLI text lost its command line: %q", cliText)
	}
}

// A file written outside the project folder, or under a non-standard name, is
// not reachable from the menu, so the document keeps the explicit command.
func TestPresentDocumentKeepsTheCommandWhenTheMenuWouldNotFindIt(t *testing.T) {
	next := "cd '/work/gen' && dcraft up -f 'stack.yaml'"
	res := core.Result{Outcome: core.OutcomeSuccess, Data: Payload{Init: Report{
		Mode: "image", Service: "app", Path: "/work/gen/stack.yaml", ProjectName: "work",
		NextCommand: next,
	}}}
	docText := core.ValueLinesText(Document(res).Blocks[0].Lines)
	if !strings.Contains(docText, next) {
		t.Fatalf("document dropped a command the menu cannot replace: %q", docText)
	}
	if strings.Contains(docText, msgDocNext) {
		t.Fatalf("document claimed a menu action that would not find the file: %q", docText)
	}
}
