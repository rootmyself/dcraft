package rollback

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

func documentResult() core.Result {
	return core.Result{
		Outcome: core.OutcomeSuccess,
		Action:  "rollback",
		Target:  core.Target{ProjectDir: "/work/acme-platform"},
		Data: Payload{Rollback: Report{
			ProjectName: "acme-platform",
			Files:       []string{"/work/acme-platform/compose.yaml"},
			Services:    []string{"api"},
			Verified:    true,
			Images: []stack.RollbackPin{
				{Service: "api", ImageID: "sha256:1111", RetentionTag: "dcraft-good-1"},
			},
			Readiness: []stack.ServiceReadiness{
				{Service: "api", Status: stack.ReadinessHealthy},
			},
		}},
	}
}

// The interactive rollback screen is a structured document, not the CLI string.
// Its next actions name the menu entries, because the reader there does not type
// commands.
func TestDocumentNextActionsUseTheMenu(t *testing.T) {
	doc := Document(documentResult())
	if len(doc.Blocks) == 0 {
		t.Fatal("empty document")
	}
	var callout string
	for _, b := range doc.Blocks {
		if b.Kind == core.BlockCallout && b.Callout == core.CalloutInfo {
			callout = b.Text
		}
	}
	if callout == "" {
		t.Fatal("no next-action callout")
	}
	if strings.Contains(callout, "dcraft ") {
		t.Fatalf("the document tells an interactive reader to type a command:\n%s", callout)
	}
	if !strings.Contains(callout, "메뉴") {
		t.Fatalf("the callout does not say where to act:\n%s", callout)
	}
}

// The CLI presenter keeps the command lines it always had. The two surfaces
// diverge on purpose, and this pins the CLI half.
func TestPresentKeepsTheCommandLines(t *testing.T) {
	out := Present(documentResult(), 0)
	for _, want := range []string{"다음 작업", "dcraft status로 실행 상태 확인."} {
		if !strings.Contains(out, want) {
			t.Fatalf("CLI output lost %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "메뉴에서") {
		t.Fatalf("CLI output adopted the interactive wording:\n%s", out)
	}
}

// An unverified rollback says so instead of claiming the app is healthy.
func TestDocumentDoesNotClaimVerifiedWhenItIsNot(t *testing.T) {
	res := documentResult()
	payload := res.Data.(Payload)
	payload.Rollback.Verified = false
	res.Data = payload

	doc := Document(res)
	if len(doc.Blocks) == 0 || doc.Blocks[0].Kind != core.BlockBadges {
		t.Fatalf("first block = %+v, want badges", doc.Blocks)
	}
	badges := doc.Blocks[0].Badges
	if len(badges) != 1 {
		t.Fatalf("badges = %+v", badges)
	}
	if badges[0].Kind == core.BadgeRunning && badges[0].Text == msgSuccessHeader {
		t.Fatalf("an unverified rollback claimed completion: %+v", badges[0])
	}
	if badges[0].Text != msgSuccessUnverified {
		t.Fatalf("badge text = %q, want %q", badges[0].Text, msgSuccessUnverified)
	}
}
