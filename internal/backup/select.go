package backup

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/rootmyself/dcraft/internal/data"
)

// UnknownVolumeError reports a requested key/name that is not part of the
// selected stack. Available lists the selectable Compose keys.
type UnknownVolumeError struct {
	Requested []string
	Available []string
}

func (e *UnknownVolumeError) Error() string {
	return fmt.Sprintf("unknown data volume %q (available: %s)",
		strings.Join(e.Requested, ", "), strings.Join(e.Available, ", "))
}

// UnsupportedSourceError reports an anonymous volume or a bind folder that
// ticket #16 cannot archive. The message is deliberately explicit instead of
// silently skipping the data.
type UnsupportedSourceError struct {
	Reference string
	Kind      data.Kind
	Anonymous bool
}

func (e *UnsupportedSourceError) Error() string {
	if e.Anonymous {
		return fmt.Sprintf("anonymous volume %q has no stable name and cannot be backed up by this command", e.Reference)
	}
	if e.Kind == data.KindBind {
		return fmt.Sprintf("%q is a bind-mounted host folder; select it with --bind instead of --volume", e.Reference)
	}
	return fmt.Sprintf("data source %q is not a named volume", e.Reference)
}

// InvalidVolumeNameError protects the docker -v argument from unexpected
// characters in a declared external volume name.
type InvalidVolumeNameError struct{ Name string }

func (e *InvalidVolumeNameError) Error() string {
	return fmt.Sprintf("volume name %q contains unsupported characters", e.Name)
}

var volumeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// SelectVolumeSources resolves explicit user selection. Values match a Compose
// key first, then the actual engine name. Anonymous volumes and bind sources
// are rejected with an explicit error, never skipped.
func SelectVolumeSources(sources []data.DataSource, requested []string) ([]data.DataSource, error) {
	if len(requested) == 0 {
		return nil, ErrVolumeRequired
	}
	byKey := map[string]data.DataSource{}
	byName := map[string]data.DataSource{}
	var available []string
	for _, src := range sources {
		if src.Kind == data.KindVolume && !src.Anonymous && src.Key != "" {
			byKey[src.Key] = src
			available = append(available, src.Key)
		}
		if src.Kind == data.KindVolume && !src.Anonymous && src.Name != "" {
			byName[src.Name] = src
		}
	}

	seen := map[string]bool{}
	seenRequested := map[string]bool{}
	var out []data.DataSource
	for _, raw := range requested {
		ref := strings.TrimSpace(raw)
		if ref == "" || seenRequested[ref] {
			continue
		}
		seenRequested[ref] = true
		src, ok := byKey[ref]
		if !ok {
			src, ok = byName[ref]
		}
		if !ok {
			// An explicitly requested anonymous/bind reference is reported
			// accurately instead of being treated as a typo.
			for _, candidate := range sources {
				if candidate.Anonymous && contains(candidate.MountTargets, ref) {
					return nil, &UnsupportedSourceError{Reference: ref, Kind: candidate.Kind, Anonymous: true}
				}
				if candidate.Kind == data.KindBind && candidate.Name == ref {
					return nil, &UnsupportedSourceError{Reference: ref, Kind: candidate.Kind}
				}
			}
			return nil, &UnknownVolumeError{Requested: []string{ref}, Available: available}
		}
		if src.Anonymous || src.Kind != data.KindVolume {
			return nil, &UnsupportedSourceError{Reference: ref, Kind: src.Kind, Anonymous: src.Anonymous}
		}
		if !volumeNameRe.MatchString(src.Name) {
			return nil, &InvalidVolumeNameError{Name: src.Name}
		}
		if seen[src.Key] {
			continue
		}
		seen[src.Key] = true
		out = append(out, src)
	}
	if len(out) == 0 {
		return nil, ErrVolumeRequired
	}
	return out, nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// SelectableVolumes keeps only named volumes, in backend order. It is used by
// interactive selection; unsupported sources are displayed separately by the
// caller's guidance instead of being offered as a valid choice.
func SelectableVolumes(sources []data.DataSource) []data.DataSource {
	var out []data.DataSource
	for _, src := range sources {
		if src.Kind == data.KindVolume && !src.Anonymous && src.Key != "" {
			out = append(out, src)
		}
	}
	return out
}

// SourceFrom resolves the manifest identity of one selected volume.
func SourceFrom(src data.DataSource, project, environment, projectDir string) Source {
	source := Source{
		Kind:         src.Kind,
		Project:      project,
		Environment:  environment,
		ProjectDir:   projectDir,
		VolumeKey:    src.Key,
		MountTargets: append([]string{}, src.MountTargets...),
		External:     src.External,
		Anonymous:    src.Anonymous,
		Services:     append([]string{}, src.Services...),
	}
	if src.Kind == data.KindBind {
		source.HostPath = src.Name
	} else {
		source.VolumeName = src.Name
	}
	return source
}
