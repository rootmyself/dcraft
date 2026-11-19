package initconfig

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Spec is the generated, validated-ready Compose file and the effects shown to
// the user. It contains no resolved secret values and no inferred runtime
// settings.
type Spec struct {
	Input        Input
	Destination  Destination
	Content      string
	NamedVolumes []string
	Binds        []Bind
	Resources    []Resource
	Notices      []string
}

// Bind is one host bind mount the generated file references. dcraft never
// creates these host paths.
type Bind struct {
	HostPath string
	Target   string
	Home     bool
}

// Resource is one file or Compose resource the operation touches or the later
// `dcraft up` may create.
type Resource struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
}

type composeDocument struct {
	Services map[string]composeService `yaml:"services"`
	Volumes  map[string]composeVolume  `yaml:"volumes,omitempty"`
}

type composeService struct {
	Image       string            `yaml:"image,omitempty"`
	Build       *composeBuild     `yaml:"build,omitempty"`
	Ports       []string          `yaml:"ports,omitempty"`
	Volumes     []string          `yaml:"volumes,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Command     string            `yaml:"command,omitempty"`
	Restart     string            `yaml:"restart,omitempty"`
}

type composeBuild struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

type composeVolume struct{}

func buildSpec(projectDir string, input Input, dest Destination) (*Spec, error) {
	spec := &Spec{Input: input, Destination: dest}
	service := composeService{
		Ports:   append([]string{}, input.Ports...),
		Command: input.Command,
		Restart: input.Restart,
	}
	env, err := parseEnvironment(input.Env)
	if err != nil {
		return nil, err
	}
	if len(env) > 0 {
		service.Environment = env
	}

	named := map[string]bool{}
	for _, raw := range input.Volumes {
		volume, err := parseVolume(raw)
		if err != nil {
			return nil, err
		}
		emitted := raw
		if volume.Bind {
			bind := Bind{Target: volume.Target, Home: volume.Home}
			switch {
			case volume.Home:
				bind.HostPath = volume.Source
			case filepath.IsAbs(volume.Source):
				bind.HostPath = filepath.Clean(volume.Source)
			default:
				abs := filepath.Clean(filepath.Join(projectDir, volume.Source))
				rel, relErr := relativePath(dest.Dir, abs)
				if relErr != nil {
					return nil, inputErrDetail(msgVolumeInvalid, fmt.Sprintf("볼륨: %q (저장 위치 기준 상대 경로를 만들 수 없습니다)", raw))
				}
				bind.HostPath = abs
				emitted = joinVolume(rel, volume.Target, volume.Mode)
			}
			spec.Binds = append(spec.Binds, bind)
		}
		if volume.Named {
			named[volume.Source] = true
		}
		if volume.Source == "" {
			spec.Resources = append(spec.Resources, Resource{
				Kind: "anonymous_volume", Name: volume.Target, Detail: "dcraft up 시 Compose가 익명 볼륨을 만들 수 있습니다",
			})
		}
		service.Volumes = append(service.Volumes, emitted)
	}

	document := composeDocument{Services: map[string]composeService{input.Service: service}}
	if len(named) > 0 {
		document.Volumes = make(map[string]composeVolume, len(named))
		for name := range named {
			document.Volumes[name] = composeVolume{}
		}
		spec.NamedVolumes = make([]string, 0, len(named))
		for name := range named {
			spec.NamedVolumes = append(spec.NamedVolumes, name)
		}
		sort.Strings(spec.NamedVolumes)
	}

	switch input.Mode {
	case ModeDockerfile:
		contextAbs := resolvePath(projectDir, input.Context)
		info, statErr := os.Stat(contextAbs)
		if statErr != nil {
			return nil, inputErrDetail(msgContextMissing, fmt.Sprintf("빌드 컨텍스트: %s", contextAbs))
		}
		if !info.IsDir() {
			return nil, inputErrDetail(msgContextNotDir, contextAbs)
		}
		dockerfileAbs := input.Dockerfile
		if !filepath.IsAbs(dockerfileAbs) {
			dockerfileAbs = filepath.Join(contextAbs, dockerfileAbs)
		}
		dockerfileAbs = filepath.Clean(dockerfileAbs)
		info, statErr = os.Stat(dockerfileAbs)
		if statErr != nil {
			return nil, inputErrDetail(msgDockerfileMiss, fmt.Sprintf("Dockerfile: %s", dockerfileAbs))
		}
		if !info.Mode().IsRegular() {
			return nil, inputErrDetail(msgDockerfileNotReg, dockerfileAbs)
		}
		contextRel, relErr := relativePath(dest.Dir, contextAbs)
		if relErr != nil {
			contextRel = filepath.ToSlash(contextAbs)
		}
		dockerfileRel, relErr := filepath.Rel(contextAbs, dockerfileAbs)
		if relErr != nil {
			dockerfileRel = filepath.ToSlash(dockerfileAbs)
		}
		service.Build = &composeBuild{
			Context:    filepath.ToSlash(contextRel),
			Dockerfile: filepath.ToSlash(dockerfileRel),
		}
		document.Services = map[string]composeService{input.Service: service}
		spec.Notices = append(spec.Notices, msgNoticeExpose)
	case ModeImage:
		service.Image = input.Image
		document.Services = map[string]composeService{input.Service: service}
	default:
		return nil, inputErr(msgModeInvalid)
	}

	content, err := renderCompose(document)
	if err != nil {
		return nil, inputErrDetail(msgOutputInvalid, err.Error())
	}
	spec.Content = content

	spec.Resources = append(spec.Resources, Resource{
		Kind: "file", Name: dest.Path, Detail: "dcraft가 저장하는 파일",
	})
	for _, name := range spec.NamedVolumes {
		spec.Resources = append(spec.Resources, Resource{
			Kind: "named_volume", Name: name, Detail: "dcraft up 시 Compose가 생성할 수 있으며 dcraft init은 만들지 않습니다",
		})
	}
	for _, bind := range spec.Binds {
		detail := "호스트 경로를 사용하며 dcraft는 만들지 않습니다. 없으면 Compose/Docker가 실행 시 만들 수 있습니다"
		if bind.Home {
			detail = "~ 경로는 Compose가 홈 디렉터리로 확장하며 dcraft는 만들지 않습니다"
		}
		spec.Resources = append(spec.Resources, Resource{
			Kind: "bind", Name: bind.HostPath, Detail: detail,
		})
	}
	if len(env) > 0 {
		spec.Notices = append(spec.Notices, msgNoticeEnvPlain)
	}
	if dest.Outside {
		spec.Notices = append(spec.Notices, msgNoticeOutside)
	}
	spec.Notices = append(spec.Notices, msgNoticeEditable)
	return spec, nil
}

func renderCompose(document composeDocument) (string, error) {
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return "", err
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func resolvePath(projectDir, raw string) string {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	return filepath.Clean(filepath.Join(projectDir, raw))
}

// relativePath renders a path relative to the Compose file's directory. A
// relative bind source must keep its "." prefix or Compose would read it as a
// named volume instead of a host path.
func relativePath(fromDir, target string) (string, error) {
	rel, err := filepath.Rel(fromDir, target)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == "" {
		return ".", nil
	}
	if rel != "." && !strings.HasPrefix(rel, ".") && !filepath.IsAbs(rel) {
		rel = "./" + rel
	}
	return rel, nil
}

func joinVolume(source, target, mode string) string {
	value := source + ":" + target
	if mode != "" {
		value += ":" + mode
	}
	return value
}
