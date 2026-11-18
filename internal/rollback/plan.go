package rollback

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/state"
)

// Plan is the read-only preview of a rollback. It is a step plan, never an
// exact future-state diff.
type Plan struct {
	Text string `json:"text"`
}

// Preview resolves the same record and prechecks as Run but changes nothing: no
// lock, no apply, no record write. Both interfaces show it before the
// independent previous_version approval is collected.
func (c *Controller) Preview(ctx context.Context, in core.Invocation) (Plan, error) {
	if c == nil || c.updater == nil || c.applier == nil || c.identity == nil || c.store == nil {
		return Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoController,
			NextActions: []string{msgNoControllerAct},
		})
	}
	ls, ok := c.store.(lockStore)
	if !ok {
		return Plan{}, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoState,
			NextActions: []string{msgNoStateAct},
		})
	}
	rec, found, err := ls.LoadGoodVersion(in.Target)
	if err != nil {
		return Plan{}, core.FailCause(err, stateFailedDiagnostic(err))
	}
	if !found {
		return Plan{}, core.Fail(noRecordDiagnostic(in.Target))
	}
	if err := validateRecord(rec, in.Target); err != nil {
		return Plan{}, err
	}
	p, err := c.precheck(ctx, in, rec)
	if err != nil {
		return Plan{}, err
	}
	return Plan{Text: planText(rec, p)}, nil
}

func planText(rec state.GoodVersion, p plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", msgPlanHeader)
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessFolder, rec.Target.ProjectDir)
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessFiles, strings.Join(rec.Target.Files, ", "))
	if len(rec.Target.EnvFiles) > 0 {
		fmt.Fprintf(&b, "  %s: %s\n", msgSuccessEnvFiles, strings.Join(rec.Target.EnvFiles, ", "))
	}
	if len(rec.Target.Profiles) > 0 {
		fmt.Fprintf(&b, "  %s: %s\n", msgSuccessProfiles, strings.Join(rec.Target.Profiles, ", "))
	}
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessProject, rec.Target.ProjectName)
	if rec.Target.Environment != "" {
		fmt.Fprintf(&b, "  %s: %s\n", msgSuccessEnv, rec.Target.Environment)
	}
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessRecorded, rec.RecordedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "  %s: %s\n", msgSuccessServices, strings.Join(p.services, ", "))
	b.WriteString("  " + msgPlanImages + ":\n")
	for _, pin := range p.pins {
		fmt.Fprintf(&b, "    - %s: %s (태그: %s)\n", pin.Service, pin.ImageID, pin.RetentionTag)
	}
	fmt.Fprintf(&b, "  %s: %s\n", msgPlanApply, msgPlanApplyValue)
	fmt.Fprintf(&b, "  %s: %s (%s, %s)\n", msgPlanApproval, core.ApprovalPreviousGood.KoreanLabel(), core.ApprovalPreviousGood, "previous_version")
	b.WriteString(msgPlanInterrupt + "\n")
	fmt.Fprintf(&b, "  %s: %s\n", msgPlanNotRestored, msgPlanNotRestoredValue)
	b.WriteString(msgPlanRecordNote + "\n")
	if p.alreadyAtVersion {
		b.WriteString(msgPlanRepeatNote + "\n")
	}
	for _, notice := range p.notices {
		if notice == msgSuccessAlready {
			continue
		}
		b.WriteString("  " + notice + "\n")
	}
	b.WriteString(msgPlanDisclaimer)
	return b.String()
}
