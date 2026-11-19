package core

import "context"

// ApprovalKind identifies a distinct approval that cannot be reused for a
// different kind of risk. The engine refuses to run a feature without the
// matching explicit approval; interactive UIs only collect it.
type ApprovalKind string

const (
	ApprovalNone ApprovalKind = ""
	// ApprovalInstall covers installing a missing Compose distribution.
	ApprovalInstall ApprovalKind = "install"
	// ApprovalScriptTrust covers first execution or changed host scripts.
	ApprovalScriptTrust ApprovalKind = "script_trust"
	// ApprovalPreviousGood covers returning to the previous good deployment.
	ApprovalPreviousGood ApprovalKind = "previous_version"
	// ApprovalDataRestore covers replacing data from a backup.
	ApprovalDataRestore ApprovalKind = "data_restore"
	// ApprovalFileOverwrite covers replacing existing generated files.
	ApprovalFileOverwrite ApprovalKind = "file_overwrite"
)

// KoreanLabel is used in Korean guidance. Machine values stay English.
func (a ApprovalKind) KoreanLabel() string {
	switch a {
	case ApprovalInstall:
		return "설치"
	case ApprovalScriptTrust:
		return "스크립트 신뢰"
	case ApprovalPreviousGood:
		return "이전 버전 복원"
	case ApprovalDataRestore:
		return "데이터 복원"
	case ApprovalFileOverwrite:
		return "파일 덮어쓰기"
	default:
		return "추가"
	}
}

// ConditionalApproval is an optional feature capability for independent
// approvals that are only necessary in some situations (for example replacing
// an existing generated file). The feature inspects the resolved target and its
// options without changing anything; the engine enforces the returned kind
// exactly like Descriptor.Approval, sets Result.RequiredApproval and never runs
// the feature without it. The invocation is built before preflight, so only
// Target, Services, Args, Options, OptionLists, Interactive, IO, JSON, Clock
// and State are populated. A read-only wizard "steps" preview is exempt so the
// interactive flow can show the effect and collect the approval for its
// "apply" run.
type ConditionalApproval interface {
	RequiredApproval(ctx context.Context, in Invocation) (ApprovalKind, error)
}

// HasApproval reports whether the caller supplied the required approval.
func HasApproval(approvals []ApprovalKind, required ApprovalKind) bool {
	if required == ApprovalNone {
		return true
	}
	for _, a := range approvals {
		if a == required {
			return true
		}
	}
	return false
}
