package target

import (
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/environments"
)

// environmentRequired rejects an operation when aliases are registered and the
// caller did not select one. It never picks the first, prod or a remembered
// alias, and the common engine turns the category into exit code 2.
func environmentRequired(names []string) error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryEnvironmentRequired,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgEnvironmentRequired, strings.Join(names, ", ")),
		Detail:      strings.Join(names, ", "),
		NextActions: []string{msgEnvironmentRequiredActionOne, msgEnvironmentRequiredActionTwo, msgEnvironmentRequiredActionThree},
	})
}

func environmentInputConflict() error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryEnvironmentConflict,
		Severity:    core.SeverityError,
		Message:     msgEnvironmentInputConflict,
		NextActions: []string{msgEnvironmentInputConflictActionOne, msgEnvironmentInputConflictActionTwo},
	})
}

func environmentUnknownWithoutAliases(name string) error {
	return core.Fail(core.Diagnostic{
		Category:    core.CategoryEnvironmentUnknown,
		Severity:    core.SeverityError,
		Message:     fmt.Sprintf(msgEnvironmentUnknownNone, name),
		Detail:      name,
		NextActions: []string{msgEnvironmentUnknownNoneActionOne, msgEnvironmentUnknownNoneActionTwo},
	})
}

// environmentLimitsDiagnostic is always shown for a selected alias so the user
// never reads "environment" as complete isolation of host resources.
func environmentLimitsDiagnostic() core.Diagnostic {
	return core.Diagnostic{
		Category:    core.CategoryEnvironmentLimits,
		Severity:    core.SeverityInfo,
		Message:     msgEnvironmentLimits,
		NextActions: []string{msgEnvironmentLimitsAction},
	}
}

// environmentWarnings detects visible explicit-project-name sharing and static
// host-resource overlaps across the aliases of this folder. It never changes
// the target and never claims complete isolation.
func environmentWarnings(catalog environments.Catalog, selected core.Environment, tgt core.Target, explicitProjectName string) []core.Diagnostic {
	var warnings []core.Diagnostic
	selectedDecls, selectedErr := environments.ReadDeclarations(tgt.Files)

	projectName := explicitProjectName
	if projectName == "" && selectedErr == nil {
		projectName = selectedDecls.ProjectName
	}
	if projectName != "" && len(catalog.Environments) > 1 {
		shared := []string{selected.Name}
		for _, other := range catalog.Environments {
			if other.Name == selected.Name {
				continue
			}
			if explicitProjectName != "" {
				shared = append(shared, other.Name)
				continue
			}
			otherFiles := filesForAlias(other, tgt.ProjectDir)
			decls, err := environments.ReadDeclarations(otherFiles)
			if err != nil || decls.ProjectName == "" || decls.ProjectName != projectName {
				continue
			}
			shared = append(shared, other.Name)
		}
		if len(shared) > 1 {
			warnings = append(warnings, core.Diagnostic{
				Category:    core.CategoryProjectNameShared,
				Severity:    core.SeverityWarning,
				Message:     fmt.Sprintf(msgProjectNameShared, quoteNames(shared), projectName),
				Detail:      projectName + ": " + strings.Join(shared, ", "),
				NextActions: []string{msgProjectNameSharedActionOne, msgProjectNameSharedActionTwo},
			})
		}
	}

	if selectedErr != nil {
		return warnings
	}
	for _, other := range catalog.Environments {
		if other.Name == selected.Name {
			continue
		}
		otherFiles := filesForAlias(other, tgt.ProjectDir)
		otherDecls, err := environments.ReadDeclarations(otherFiles)
		if err != nil {
			continue
		}
		conflicts := environments.Compare(selectedDecls, otherDecls)
		if len(conflicts) == 0 {
			continue
		}
		var lines []string
		for _, c := range conflicts {
			line := c.Kind + " " + c.Resource
			if c.Service != "" || c.Other != "" {
				line += ": " + c.Service + " ↔ " + c.Other
			}
			lines = append(lines, line)
		}
		warnings = append(warnings, core.Diagnostic{
			Category:    core.CategoryEnvironmentOverlap,
			Severity:    core.SeverityWarning,
			Message:     fmt.Sprintf(msgEnvironmentOverlap, selected.Name, other.Name),
			Detail:      strings.Join(lines, "\n"),
			NextActions: []string{msgEnvironmentOverlapActionOne, msgEnvironmentOverlapActionTwo},
		})
	}
	return warnings
}

func quoteNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "'"+name+"'")
	}
	return strings.Join(quoted, ", ")
}

// filesForAlias returns the alias Compose files, falling back to the same
// standard discovery the alias itself would use. It only feeds warnings; the
// real selection path validates files before this point.
func filesForAlias(env core.Environment, projectDir string) []string {
	if len(env.Files) > 0 {
		return env.Files
	}
	files, _, err := discoverIn(projectDir)
	if err != nil {
		return nil
	}
	return files
}
