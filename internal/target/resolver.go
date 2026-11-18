// Package target resolves the stack target strictly from a project folder.
// It never searches parent folders, and it never writes or modifies Compose
// files. Explicit inputs follow Compose's own relative-path convention: they
// are resolved against the process working directory.
package target

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/environments"
)

// Resolver implements core.Resolver.
type Resolver struct{}

func New() *Resolver { return &Resolver{} }

// defaultBaseFiles is Compose's documented preference order. Compose warns and
// uses the first match when several exist; dcraft does the same and surfaces a
// warning diagnostic instead of failing.
var defaultBaseFiles = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yml",
	"docker-compose.yaml",
}

// Environments implements core.EnvironmentSource. It lists aliases only; it
// does not validate every referenced file, so one unfinished alias does not
// hide the others in the TUI selection screen.
func (r *Resolver) Environments(ctx context.Context, req core.TargetRequest, cwd string) ([]core.Environment, []core.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, nil, core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryTargetInvalid,
				Severity:    core.SeverityError,
				Message:     msgWorkingDirUnknown,
				NextActions: []string{msgWorkingDirAction},
			})
		}
	}
	projectDir, err := resolveProjectDir(req.Path, cwd)
	if err != nil {
		return nil, nil, err
	}
	catalog, err := environments.Load(projectDir)
	if err != nil {
		return nil, nil, err
	}
	return catalog.Environments, nil, nil
}

// Resolve implements core.Resolver.
func (r *Resolver) Resolve(ctx context.Context, req core.TargetRequest, cwd string) (core.Target, []core.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return core.Target{}, nil, err
	}
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return core.Target{}, nil, core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryTargetInvalid,
				Severity:    core.SeverityError,
				Message:     msgWorkingDirUnknown,
				NextActions: []string{msgWorkingDirAction},
			})
		}
	}

	projectDir, err := resolveProjectDir(req.Path, cwd)
	if err != nil {
		return core.Target{ProjectDir: projectDir, Environment: req.Environment}, nil, err
	}

	tgt := core.Target{
		ProjectDir:  projectDir,
		Files:       []string{},
		EnvFiles:    []string{},
		Profiles:    append([]string{}, req.Profiles...),
		ProjectName: req.ProjectName,
		Environment: req.Environment,
	}

	catalog, err := environments.Load(projectDir)
	if err != nil {
		return tgt, nil, err
	}

	var warnings []core.Diagnostic
	if len(catalog.Environments) > 0 {
		if req.Environment == "" {
			return tgt, nil, environmentRequired(catalog.Names())
		}
		if len(req.Files) > 0 || len(req.EnvFiles) > 0 || len(req.Profiles) > 0 {
			return tgt, nil, environmentInputConflict()
		}
		env, err := catalog.Expand(req.Environment)
		if err != nil {
			return tgt, nil, err
		}
		tgt.Environment = env.Name
		tgt.Files = env.Files
		tgt.EnvFiles = env.EnvFiles
		tgt.Profiles = env.Profiles
		if len(tgt.Files) == 0 {
			files, discoveredWarnings, err := discoverIn(projectDir, req.AllowMissingConfiguration)
			if err != nil {
				return tgt, nil, err
			}
			tgt.Files = files
			warnings = append(warnings, discoveredWarnings...)
		}
		if len(tgt.EnvFiles) == 0 && len(tgt.Files) > 0 {
			candidate := filepath.Join(filepath.Dir(tgt.Files[0]), ".env")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				tgt.DefaultEnvFile = candidate
			}
		}
		warnings = append(warnings, environmentWarnings(catalog, env, tgt, req.ProjectName)...)
		warnings = append(warnings, environmentLimitsDiagnostic())
		return tgt, warnings, nil
	}

	if req.Environment != "" {
		return tgt, nil, environmentUnknownWithoutAliases(req.Environment)
	}

	if len(req.Files) > 0 {
		files, err := resolveExplicitFiles(req.Files, cwd, core.CategoryTargetInvalid, msgComposeFileMissing, msgComposeFileActions)
		if err != nil {
			return tgt, warnings, err
		}
		tgt.Files = files
	} else {
		files, discoveredWarnings, err := discoverIn(projectDir, req.AllowMissingConfiguration)
		if err != nil {
			return tgt, warnings, err
		}
		tgt.Files = files
		warnings = append(warnings, discoveredWarnings...)
	}

	if len(req.EnvFiles) > 0 {
		envFiles, err := resolveExplicitFiles(req.EnvFiles, cwd, core.CategoryTargetInvalid, msgEnvFileMissing, msgEnvFileActions)
		if err != nil {
			return tgt, warnings, err
		}
		tgt.EnvFiles = envFiles
	} else if len(tgt.Files) > 0 {
		// Compose loads .env from the first Compose file's directory when no
		// explicit --env-file is given. Report it so users see the real input.
		candidate := filepath.Join(filepath.Dir(tgt.Files[0]), ".env")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			tgt.DefaultEnvFile = candidate
		}
	}

	return tgt, warnings, nil
}

func resolveProjectDir(path, cwd string) (string, error) {
	dir := path
	if dir == "" {
		dir = cwd
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	dir = filepath.Clean(dir)

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return dir, core.FailCause(err, core.Diagnostic{
				Category:    core.CategoryTargetInvalid,
				Severity:    core.SeverityError,
				Message:     msgTargetMissing,
				Detail:      dir,
				NextActions: []string{msgTargetMissingAction},
			})
		}
		return dir, core.FailCause(err, core.Diagnostic{
			Category:    core.CategoryTargetInvalid,
			Severity:    core.SeverityError,
			Message:     msgTargetUnreadable,
			Detail:      err.Error(),
			NextActions: []string{msgTargetUnreadableAction},
		})
	}
	if !info.IsDir() {
		return dir, core.Fail(core.Diagnostic{
			Category:    core.CategoryTargetInvalid,
			Severity:    core.SeverityError,
			Message:     msgTargetNotDirectory,
			Detail:      dir,
			NextActions: []string{msgTargetNotDirectoryAction},
		})
	}
	return dir, nil
}

func resolveExplicitFiles(paths []string, cwd string, category core.Category, missingMsg string, actions []string) ([]string, error) {
	resolved := make([]string, 0, len(paths))
	for _, raw := range paths {
		p := raw
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		p = filepath.Clean(p)
		info, err := os.Stat(p)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, core.FailCause(err, core.Diagnostic{
					Category:    category,
					Severity:    core.SeverityError,
					Message:     missingMsg,
					Detail:      fmt.Sprintf("%s (%s)", raw, p),
					NextActions: append([]string{}, actions...),
				})
			}
			return nil, core.FailCause(err, core.Diagnostic{
				Category:    category,
				Severity:    core.SeverityError,
				Message:     msgExplicitUnreadable,
				Detail:      err.Error(),
				NextActions: append([]string{}, actions...),
			})
		}
		if info.IsDir() {
			return nil, core.Fail(core.Diagnostic{
				Category:    category,
				Severity:    core.SeverityError,
				Message:     msgExplicitNotFile,
				Detail:      p,
				NextActions: append([]string{}, actions...),
			})
		}
		resolved = append(resolved, p)
	}
	return resolved, nil
}

// discoverIn looks only inside dir. It returns the base file plus the matching
// override, in Compose order. When allowMissing is set (a feature that does not
// operate on a Compose project), a folder without Compose files is a valid
// empty selection instead of a no_configuration failure.
func discoverIn(dir string, allowMissing bool) ([]string, []core.Diagnostic, error) {
	var present []string
	for _, name := range defaultBaseFiles {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err == nil && !info.IsDir() {
			present = append(present, p)
		}
	}
	if len(present) == 0 {
		if allowMissing {
			return []string{}, nil, nil
		}
		return nil, nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryNoConfiguration,
			Severity:    core.SeverityError,
			Message:     msgNoConfiguration,
			Detail:      dir,
			NextActions: []string{msgNoConfigurationActionOne, msgNoConfigurationActionTwo, msgNoConfigurationActionThree},
		})
	}

	base := present[0]
	files := []string{base}
	ext := filepath.Ext(base)
	override := filepath.Join(dir, strings.TrimSuffix(filepath.Base(base), ext)+".override"+ext)
	if info, err := os.Stat(override); err == nil && !info.IsDir() {
		files = append(files, override)
	}

	var warnings []core.Diagnostic
	if len(present) > 1 {
		warnings = append(warnings, core.Diagnostic{
			Category: core.CategoryMultipleConfigFiles,
			Severity: core.SeverityWarning,
			Message:  msgMultipleConfigFiles,
			Detail:   "using " + base + "; also found " + strings.Join(present[1:], ", "),
			NextActions: []string{
				msgMultipleConfigFilesAction,
			},
		})
	}
	return files, warnings, nil
}
