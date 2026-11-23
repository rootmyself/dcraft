package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const hookOutputLimit = 4000

// prepareHooks is the engine-owned host-script seam. It runs only for features
// that declare a HookScope. The order is fixed: plan + independent trust check,
// pre scripts, re-resolve/re-validate the actual inputs, then the feature. Post
// scripts are handled after verification by runHooksPost.
func (e *Engine) prepareHooks(ctx context.Context, rec *recorder, req Request, desc Descriptor, clock Clock, res *Result, target *Target) (HookPlan, error) {
	if desc.Hooks == HookNone {
		return HookPlan{}, nil
	}
	if e.Hooks == nil {
		return HookPlan{}, Fail(Diagnostic{
			Category:    CategoryInternal,
			Severity:    SeverityError,
			Message:     "호스트 스크립트 연결이 준비되지 않았음.",
			Detail:      "hook service is not configured",
			NextActions: []string{"dcraft를 다시 빌드하거나 설치한 뒤 실행."},
		})
	}
	plan, err := e.Hooks.Plan(ctx, *target)
	if err != nil {
		// Surface a failed pre-hook stage even when planning itself failed,
		// because the main operation must not start.
		_ = RunStage(rec, StageNameHooksPre, msgStageHooksPre, func() error { return err })
		return HookPlan{}, err
	}
	if len(plan.Entries) == 0 {
		return plan, nil
	}
	if plan.RequiresApproval {
		approved := false
		if len(req.ApprovedHooks) > 0 {
			if changed := firstUnapprovedChange(req.ApprovedHooks, plan); changed != nil {
				return plan, Fail(Diagnostic{
					Category: CategoryHookContentChanged,
					Severity: SeverityError,
					Message: fmt.Sprintf(
						"승인 화면에서 확인한 뒤 스크립트 내용이 바뀌었음: %s (%s). 이전 승인을 재사용하지 않음.",
						changed.Path, changed.Stage),
					Detail: fmt.Sprintf("approved_sha256=%s current_sha256=%s", changed.ApprovedSHA256, changed.SHA256),
					NextActions: []string{
						"스크립트 내용을 다시 확인한 뒤 새로 승인.",
						"dcraft는 이 스크립트를 실행하지 않았고 본 작업도 시작하지 않았음.",
					},
				})
			}
			approved = true
		} else if HasApproval(req.Approvals, ApprovalScriptTrust) {
			approved = true
		}
		if !approved {
			res.RequiredApproval = ApprovalScriptTrust
			return plan, Fail(hookTrustDiagnostic(plan))
		}
		if err := e.Hooks.Approve(ctx, *target, plan, clock.Now()); err != nil {
			return plan, err
		}
	}

	pre := hookEntriesForStage(plan, HookStagePre)
	if len(pre) == 0 {
		return plan, nil
	}
	err = RunStage(rec, StageNameHooksPre, msgStageHooksPre, func() error {
		return e.runHookEntries(ctx, res, req.Feature, res.OperationID, *target, pre)
	})
	if err != nil {
		return plan, err
	}

	err = RunStage(rec, StageNameRevalidate, msgStageRevalidateInput, func() error {
		fresh, warnings, resolveErr := e.resolveFreshTarget(ctx, req, desc)
		if resolveErr != nil {
			return resolveErr
		}
		*target = fresh
		res.Target = fresh
		res.Diagnostics = append(res.Diagnostics, warnings...)

		// A pre-hook may also have changed the hook registrations themselves.
		// Post scripts were approved by content identity; a new or changed
		// post script needs a fresh approval before the main operation starts.
		freshPlan, planErr := e.Hooks.Plan(ctx, *target)
		if planErr != nil {
			return planErr
		}
		if changed := firstChangedPostHook(plan, freshPlan); changed != nil {
			return Fail(Diagnostic{
				Category: CategoryHookContentChanged,
				Severity: SeverityError,
				Message: fmt.Sprintf(
					"사전 스크립트가 승인 이후 후처리 스크립트를 바꿨음: %s (%s). 이전 승인을 재사용하지 않음.",
					changed.Path, changed.Stage),
				Detail: fmt.Sprintf("approved_sha256=%s current_sha256=%s", changed.ApprovedSHA256, changed.SHA256),
				NextActions: []string{
					"스크립트 내용과 등록을 확인한 뒤 다시 실행해 스크립트 신뢰를 승인.",
					"승인 없이는 본 작업도 시작하지 않았음.",
				},
			})
		}
		plan = freshPlan
		return nil
	})
	if err != nil {
		return plan, err
	}
	return plan, nil
}

// runHooksPost executes post scripts after the operation's work and
// verification succeeded. A failure is reported as a warning and never turns
// the completed work into a failure. It returns true when the caller canceled
// the post stage; the deployment itself already completed.
func (e *Engine) runHooksPost(ctx context.Context, rec *recorder, res *Result, action, operationID string, target Target, entries []HookEntry) (canceled bool) {
	if len(entries) == 0 {
		return false
	}
	err := RunStage(rec, StageNameHooksPost, msgStageHooksPost, func() error {
		return e.runHookEntries(ctx, res, action, operationID, target, entries)
	})
	if err == nil {
		return false
	}
	if isCanceled(ctx, err) {
		res.Diagnostics = append(res.Diagnostics, Diagnostic{
			Category: CategoryHookPostFailed,
			Severity: SeverityWarning,
			Message:  "본 작업은 완료되었지만 사후 스크립트가 중단되었음. 일부 외부 변경이 남아 있을 수 있음.",
			Detail:   hookErrorDetail(err),
			NextActions: []string{
				"dcraft status로 스택 상태 확인.",
				"사후 스크립트를 다시 실행하려면 작업을 다시 실행해야 하며, 배포를 자동으로 되돌리지 않음.",
			},
		})
		return true
	}
	diags := failureDiagnostics(err)
	for i := range diags {
		diags[i].Severity = SeverityWarning
		if diags[i].Category != CategoryHookContentChanged {
			diags[i].Category = CategoryHookPostFailed
		}
		if strings.TrimSpace(diags[i].Detail) == "" {
			diags[i].Detail = hookErrorDetail(err)
		}
	}
	res.Diagnostics = append(res.Diagnostics, diags...)
	return false
}

// runHookEntries executes entries sequentially. A hook failure stops the
// stage; later entries are not run and the stage/report says so implicitly by
// naming the failed script.
func (e *Engine) runHookEntries(ctx context.Context, res *Result, action, operationID string, target Target, entries []HookEntry) error {
	for _, entry := range entries {
		exec, err := e.Hooks.Execute(ctx, HookRequest{
			Action:      action,
			OperationID: operationID,
			Target:      target,
			Entry:       entry,
		})
		if err != nil {
			if isCanceled(ctx, err) {
				return err
			}
			var ce *Error
			if errors.As(err, &ce) {
				if strings.TrimSpace(exec.Output) != "" {
					appendHookOutput(res, entry, exec.Output)
				}
				return ce
			}
			return FailCause(err, Diagnostic{
				Category: CategoryHookInvalid,
				Severity: SeverityError,
				Message:  fmt.Sprintf("스크립트 실행 실패: %s (%s)", entry.Path, entry.Stage),
				Detail:   errDetail(err),
				NextActions: []string{
					"스크립트 파일의 실행 권한과 첫 줄(셔뱅)을 확인.",
					"dcraft는 인터프리터를 추측하지 않음.",
				},
			})
		}
		detail := hookExecutionDetail(entry, exec)
		if exec.ExitCode != 0 {
			return Fail(Diagnostic{
				Category: CategoryHookFailed,
				Severity: SeverityError,
				Message:  fmt.Sprintf("%s 스크립트가 실패: %s (종료 코드 %d)", hookStageKorean(entry.Stage), entry.Path, exec.ExitCode),
				Detail:   detail,
				NextActions: []string{
					"스크립트를 수정하거나 등록을 제거한 뒤 다시 실행.",
					"이 단계에서 실패하면 본 작업은 시작되지 않음.",
					"스크립트의 외부 변경은 dcraft가 자동으로 되돌리지 않음.",
				},
			})
		}
		if strings.TrimSpace(exec.Output) != "" {
			appendHookOutput(res, entry, exec.Output)
		}
	}
	return nil
}

func appendHookOutput(res *Result, entry HookEntry, output string) {
	output = truncateHookOutput(output)
	if output == "" {
		return
	}
	res.Diagnostics = append(res.Diagnostics, Diagnostic{
		Category: CategoryHookOutput,
		Severity: SeverityInfo,
		Message:  fmt.Sprintf("스크립트 출력: %s (%s)", entry.Path, entry.Stage),
		Detail:   output,
	})
}

func hookExecutionDetail(entry HookEntry, exec HookExecution) string {
	detail := fmt.Sprintf("path=%s stage=%s sha256=%s exit=%d", entry.Path, entry.Stage, entry.SHA256, exec.ExitCode)
	if output := truncateHookOutput(exec.Output); output != "" {
		detail += "\n" + output
	}
	return detail
}

func hookErrorDetail(err error) string {
	var ce *Error
	if errors.As(err, &ce) && len(ce.Diagnostics) > 0 {
		return ce.Diagnostics[0].Detail
	}
	return errDetail(err)
}

func truncateHookOutput(output string) string {
	output = strings.TrimRight(output, "\n")
	if len(output) > hookOutputLimit {
		output = output[:hookOutputLimit] + "…"
	}
	return output
}

func hookEntriesForStage(plan HookPlan, stage HookStage) []HookEntry {
	var out []HookEntry
	for _, entry := range plan.Entries {
		if entry.Stage == stage {
			out = append(out, entry)
		}
	}
	return out
}

type changedPostHook struct {
	Path           string
	Stage          HookStage
	ApprovedSHA256 string
	SHA256         string
}

// firstUnapprovedChange compares the hook entries the user approved with the
// entries about to run. A new or changed script is returned so the engine can
// refuse to reuse the old approval.
func firstUnapprovedChange(approved []HookEntry, current HookPlan) *changedPostHook {
	old := map[string]string{}
	for _, entry := range approved {
		old[entry.Path+"\x00"+string(entry.Stage)] = entry.SHA256
	}
	for _, entry := range current.Entries {
		sha, ok := old[entry.Path+"\x00"+string(entry.Stage)]
		if !ok || sha != entry.SHA256 {
			return &changedPostHook{Path: entry.Path, Stage: entry.Stage, ApprovedSHA256: sha, SHA256: entry.SHA256}
		}
	}
	return nil
}

func firstChangedPostHook(approved, fresh HookPlan) *changedPostHook {
	old := map[string]string{}
	for _, entry := range approved.Entries {
		if entry.Stage == HookStagePost {
			old[entry.Path] = entry.SHA256
		}
	}
	for _, entry := range fresh.Entries {
		if entry.Stage != HookStagePost {
			continue
		}
		sha, ok := old[entry.Path]
		if !ok || sha != entry.SHA256 {
			return &changedPostHook{Path: entry.Path, Stage: entry.Stage, ApprovedSHA256: sha, SHA256: entry.SHA256}
		}
	}
	return nil
}

func (e *Engine) resolveFreshTarget(ctx context.Context, req Request, desc Descriptor) (Target, []Diagnostic, error) {
	if e.Resolver == nil {
		return Target{}, nil, errors.New("target resolver is not configured")
	}
	cwd, err := e.cwd()
	if err != nil {
		return Target{}, nil, FailCause(err, Diagnostic{
			Category:    CategoryTargetInvalid,
			Severity:    SeverityError,
			Message:     msgWorkingDirUnknown,
			NextActions: []string{msgWorkingDirAction},
		})
	}
	targetReq := req.Target
	targetReq.AllowMissingConfiguration = desc.TargetOptional
	return e.Resolver.Resolve(ctx, targetReq, cwd)
}

func hookTrustDiagnostic(plan HookPlan) Diagnostic {
	var b strings.Builder
	for _, entry := range plan.Entries {
		if entry.Trusted {
			continue
		}
		state := "새 스크립트"
		if entry.Changed {
			state = "내용 변경됨"
		}
		fmt.Fprintf(&b, "- %s: %s (sha256:%s, %s)\n", entry.Stage, entry.Path, entry.SHA256, state)
	}
	return Diagnostic{
		Category: CategoryApprovalRequired,
		Severity: SeverityError,
		Message:  "등록된 호스트 스크립트에 별도의 스크립트 신뢰 승인이 필요. 승인 없이는 실행하지 않음.",
		Detail:   strings.TrimRight(b.String(), "\n"),
		NextActions: []string{
			"경로·실행 시점·내용 식별자를 확인한 뒤 자동화에서는 --approve script_trust를 명시.",
			"대화형 화면에서는 같은 계획을 확인한 뒤 승인 가능.",
			"스크립트는 사용자 권한으로 프로젝트 폴더에서 실행되며, dcraft는 샌드박싱하거나 자동으로 되돌리지 않음.",
		},
	}
}

func hookPlanText(plan HookPlan) string {
	if len(plan.Entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("호스트 스크립트 훅 (up/update 전후)\n")
	for _, entry := range plan.Entries {
		state := "승인됨"
		switch {
		case entry.Changed:
			state = "내용 변경됨 — 새 승인 필요"
		case !entry.Trusted:
			state = "새 승인 필요"
		}
		fmt.Fprintf(&b, "  - %s: %s\n    내용 식별자: sha256:%s\n    상태: %s\n", entry.Stage, entry.Path, entry.SHA256, state)
	}
	b.WriteString("스크립트는 사용자 권한으로 프로젝트 폴더에서 실행됨.\n")
	b.WriteString("일부 외부 변경이 남을 수 있고, dcraft는 샌드박싱하거나 자동으로 되돌리지 않음.\n")
	if plan.RequiresApproval {
		b.WriteString("승인: 스크립트 신뢰 (script_trust) 필요")
	}
	return strings.TrimRight(b.String(), "\n")
}

func hookStageKorean(stage HookStage) string {
	if stage == HookStagePost {
		return "사후"
	}
	return "사전"
}

func uniqueApprovals(kinds ...ApprovalKind) []ApprovalKind {
	var out []ApprovalKind
	seen := map[ApprovalKind]bool{}
	for _, kind := range kinds {
		if kind == ApprovalNone || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}
