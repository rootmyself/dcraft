package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Feature is one flat user command that both the CLI and the TUI expose. A
// feature owns its behavior, Korean text, flags and payload rendering. Adding a
// feature must not require editing another feature or the shared assembly.
type Feature interface {
	Descriptor() Descriptor
	Run(ctx context.Context, in Invocation) (any, error)
}

// Verifier is an optional feature capability. The engine runs it after the
// feature reports success and before recording history, so a feature cannot
// skip the verification stage it declares.
type Verifier interface {
	Verify(ctx context.Context, in Invocation, payload any) error
}

// Descriptor is the registration unit. The CLI derives a command from it and
// the TUI derives a screen/menu entry from it.
type Descriptor struct {
	// Name is the English command and machine action name (lowercase,
	// hyphen-separated). It is globally unique.
	Name string
	// Title, Summary and Usage are Korean user-facing text.
	Title   string
	Summary string
	Usage   string
	// Section is the TUI navigation group. It is required: a feature without
	// a declared section cannot be placed in the menu.
	Section Section
	// ReadOnly marks operations that do not change the stack. Read-only
	// features cannot require an approval.
	ReadOnly bool
	// Approval is the independent approval this operation requires. Empty
	// means no extra approval. The engine enforces it; UI only collects it.
	Approval ApprovalKind
	// RecordsHistory marks stack-changing features whose outcome is written
	// to per-stack history.
	RecordsHistory bool
	// Primary marks the feature shown when the TUI opens. Exactly one
	// registered feature may be primary.
	Primary bool
	// SupportsServices exposes the common --service flag for commands that
	// operate on explicit Compose services.
	SupportsServices bool
	// Requires lists the preflight capabilities this feature needs. Empty
	// means the full local environment check (Docker CLI, engine, Compose),
	// so existing features keep exactly today's behavior. A feature that
	// exists precisely because a capability is missing declares a narrower
	// set instead of bypassing preflight.
	Requires Requirements
	// TargetOptional marks features that do not operate on a Compose project.
	// Target resolution still validates the target folder, but a folder
	// without Compose configuration is not an error. Status and every stack
	// operation keep the default (a Compose project is required).
	TargetOptional bool
	// AcceptsArgs allows positional arguments (for example exec commands).
	AcceptsArgs bool
	// Flags are feature-owned options parsed by the shared CLI assembly.
	Flags []Flag
	// Wizard marks a feature whose TUI entry uses a guided flow instead of
	// the generic confirmation screen. The feature must implement Wizard and
	// handle Options["wizard"]="steps"/"apply" in Run.
	Wizard bool
	// EnvironmentAdmin offers alias issuance and target switching in the TUI.
	EnvironmentAdmin bool
	// StreamsProgress marks a feature that can report its own tool output
	// (currently `up`) through Invocation.IO.Progress while it runs. The CLI
	// never supplies that writer, so its captured output is unchanged.
	StreamsProgress bool
	// Hooks declares the registered host-script attachment point. The zero
	// value runs no scripts. HookUpUpdate is only valid on `up` and `update`;
	// read-only features may never declare it.
	Hooks HookScope
	// Destructive marks operations that irreversibly destroy or replace user
	// data or a running stack. It is independent of Approval: an operation
	// may need no approval but still be destructive (down), and an approval
	// may be needed without being destructive (file_overwrite, install).
	Destructive bool
}

// FlagKind is the supported shape of a feature flag. Supporting a new kind is
// a shared-contract change request, not a per-feature edit.
type FlagKind string

const (
	FlagString FlagKind = "string"
	FlagBool   FlagKind = "bool"
	// FlagList is a repeatable string flag. Values are carried separately in
	// Request.OptionLists/Invocation.OptionLists so a value containing a
	// comma or space is never re-split by the feature.
	FlagList FlagKind = "list"
)

// Flag declares a feature-owned CLI flag.
type Flag struct {
	Name      string
	Shorthand string
	Usage     string
	Kind      FlagKind
	Default   string
	// Required marks input a non-interactive run cannot invent. The CLI still
	// passes whatever was given and the feature returns its own Korean usage
	// diagnostic (exit 2) for a missing required value. Interactive
	// collection uses the generic core.Wizard flow, not a per-feature screen.
	Required bool
}

var (
	featureNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	flagNameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	shorthandRe   = regexp.MustCompile(`^[a-zA-Z]$`)
)

var reservedFlagNames = map[string]bool{
	"path": true, "file": true, "env-file": true, "profile": true,
	"project-name": true, "env": true, "json": true, "approve": true,
	"help": true, "service": true,
}

// Reserved shorthands belong to the shared CLI assembly. Features must not
// take them over.
var reservedShorthands = map[string]bool{
	"f": true, "p": true, "s": true, "h": true, "v": true,
}

func validateDescriptor(d Descriptor) error {
	if !featureNameRe.MatchString(d.Name) {
		return fmt.Errorf("feature name %q must match %s", d.Name, featureNameRe)
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("feature %q: Korean title is required", d.Name)
	}
	if strings.TrimSpace(d.Summary) == "" {
		return fmt.Errorf("feature %q: Korean summary is required", d.Name)
	}
	if strings.TrimSpace(d.Usage) == "" {
		return fmt.Errorf("feature %q: Korean usage is required", d.Name)
	}
	if d.ReadOnly && d.Approval != ApprovalNone {
		return fmt.Errorf("feature %q: read-only features cannot require approval", d.Name)
	}
	// History is the record of a stack change. A read-only feature has no
	// change to record, so the pair is contradictory rather than merely odd.
	if d.ReadOnly && d.RecordsHistory {
		return fmt.Errorf("feature %q: read-only features cannot record history", d.Name)
	}
	// The primary feature opens automatically in the TUI. A mutating primary
	// without an independent approval would run as soon as the TUI opens, so
	// it cannot be registered this way.
	if d.Primary && !d.ReadOnly && d.Approval == ApprovalNone {
		return fmt.Errorf("feature %q: a mutating primary feature requires an approval", d.Name)
	}
	switch d.Hooks {
	case HookNone, HookUpUpdate:
	default:
		return fmt.Errorf("feature %q: unknown hook scope %q", d.Name, d.Hooks)
	}
	if d.ReadOnly && d.Hooks != HookNone {
		return fmt.Errorf("feature %q: read-only features cannot run host hooks", d.Name)
	}
	// Destruction is a claim about the effect, and a read-only feature has no
	// effect. The pair contradicts itself, so the descriptor is rejected
	// instead of rendering a misleading risk badge.
	if d.ReadOnly && d.Destructive {
		return fmt.Errorf("feature %q: read-only features cannot be destructive", d.Name)
	}
	// The TUI places every feature by its declared section. An unknown or
	// empty value cannot be grouped, so it is a registration error rather than
	// a screen the user has to interpret.
	if !validSection(d.Section) {
		return fmt.Errorf("feature %q: unknown section %q", d.Name, d.Section)
	}
	for _, r := range d.Requires {
		switch r {
		case RequirementDockerCLI, RequirementEngine, RequirementCompose:
		default:
			return fmt.Errorf("feature %q: unknown preflight requirement %q", d.Name, r)
		}
	}
	seenNames := map[string]bool{}
	seenShorthands := map[string]bool{}
	for _, f := range d.Flags {
		if !flagNameRe.MatchString(f.Name) {
			return fmt.Errorf("feature %q: invalid flag name %q", d.Name, f.Name)
		}
		if reservedFlagNames[f.Name] {
			return fmt.Errorf("feature %q: flag --%s is reserved by the shared assembly", d.Name, f.Name)
		}
		if seenNames[f.Name] {
			return fmt.Errorf("feature %q: duplicate flag --%s", d.Name, f.Name)
		}
		seenNames[f.Name] = true
		if f.Shorthand != "" {
			if !shorthandRe.MatchString(f.Shorthand) {
				return fmt.Errorf("feature %q: shorthand %q must be one letter", d.Name, f.Shorthand)
			}
			lower := strings.ToLower(f.Shorthand)
			if reservedShorthands[lower] {
				return fmt.Errorf("feature %q: shorthand -%s is reserved", d.Name, f.Shorthand)
			}
			if seenShorthands[lower] {
				return fmt.Errorf("feature %q: duplicate shorthand -%s", d.Name, f.Shorthand)
			}
			seenShorthands[lower] = true
		}
		if strings.TrimSpace(f.Usage) == "" {
			return fmt.Errorf("feature %q: flag --%s needs Korean usage text", d.Name, f.Name)
		}
		switch f.Kind {
		case FlagString, FlagList:
		case FlagBool:
			switch f.Default {
			case "", "true", "false":
			default:
				return fmt.Errorf("feature %q: bool flag --%s default must be true or false", d.Name, f.Name)
			}
		default:
			return fmt.Errorf("feature %q: flag --%s has unsupported kind %q", d.Name, f.Name, f.Kind)
		}
	}
	return nil
}
