package initconfig

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// The saved-file document declares the path, the service, the project name and
// both commands; the CLI presenter keeps the same byte text.
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
		kinds[core.ValueProject] != 1 || kinds[core.ValueCommand] != 2 {
		t.Fatalf("kinds = %v, want path=1 service=1 project=1 command=2", kinds)
	}
	if got, want := Present(res, 0), strings.Join([]string{core.ValueLinesText(doc.Blocks[0].Lines), ""}, "\n"); got != want {
		t.Fatalf("presenter text = %q, want %q", got, want)
	}
}
