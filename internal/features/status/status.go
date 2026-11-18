// Package status implements the first dcraft feature: read-only stack status.
// It is the reference consumer of the common operation seam.
package status

import (
	"context"
	"fmt"
	"strings"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/stack"
)

// Feature is the registered status command.
type Feature struct {
	backend stack.Backend
}

func New(backend stack.Backend) *Feature { return &Feature{backend: backend} }

// Payload is the machine-readable data of a successful status result.
type Payload struct {
	Stack stack.Snapshot `json:"stack"`
}

func (f *Feature) Descriptor() core.Descriptor {
	return core.Descriptor{
		Name:     "status",
		Title:    msgTitle,
		Summary:  msgSummary,
		Usage:    msgUsage,
		ReadOnly: true,
		Primary:  true,
	}
}

// Run implements core.Feature.
func (f *Feature) Run(ctx context.Context, in core.Invocation) (any, error) {
	if f.backend == nil {
		return nil, core.Fail(core.Diagnostic{
			Category:    core.CategoryInternal,
			Severity:    core.SeverityError,
			Message:     msgNoBackend,
			NextActions: []string{msgNoBackendAction},
		})
	}
	snap, err := core.RunStageValue(in.Reporter, "inspect_stack", msgStageInspect, func() (stack.Snapshot, error) {
		return f.backend.Inspect(ctx, in.Target)
	})
	if err != nil {
		return nil, err
	}
	return Payload{Stack: snap}, nil
}

// Present implements core.Presenter for successful results.
func (f *Feature) Present(res core.Result, width int) string {
	payload, ok := payloadFrom(res.Data)
	if !ok {
		return msgNoPayload
	}
	snap := payload.Stack

	var b strings.Builder
	writeHeader(&b, res, snap)
	b.WriteString("\n")
	writeSummary(&b, snap)
	b.WriteString("\n")
	if len(snap.Services) == 0 {
		b.WriteString(msgNoServices)
		b.WriteString("\n\n")
	} else {
		for i, svc := range snap.Services {
			if i > 0 {
				b.WriteString("\n")
			}
			writeService(&b, svc)
		}
		b.WriteString("\n")
	}
	writeGuidance(&b, width)
	return strings.TrimRight(b.String(), "\n")
}

func payloadFrom(data any) (Payload, bool) {
	switch v := data.(type) {
	case Payload:
		return v, true
	case *Payload:
		if v != nil {
			return *v, true
		}
	}
	return Payload{}, false
}

func writeHeader(b *strings.Builder, res core.Result, snap stack.Snapshot) {
	name := snap.ProjectName
	if name == "" {
		name = msgUnknownProject
	}
	fmt.Fprintf(b, "프로젝트: %s\n", name)
	dir := snap.ProjectDir
	if dir == "" {
		dir = res.Target.ProjectDir
	}
	if dir != "" {
		fmt.Fprintf(b, "폴더: %s\n", dir)
	}
	fmt.Fprintf(b, "Compose 파일:\n")
	if len(res.Target.Files) == 0 {
		fmt.Fprintf(b, "  - %s\n", msgNone)
	} else {
		for _, f := range res.Target.Files {
			fmt.Fprintf(b, "  - %s\n", f)
		}
	}
	if len(res.Target.EnvFiles) > 0 {
		fmt.Fprintf(b, "환경 파일: %s\n", strings.Join(res.Target.EnvFiles, ", "))
	} else if res.Target.DefaultEnvFile != "" {
		fmt.Fprintf(b, "환경 파일: %s (자동 적용)\n", res.Target.DefaultEnvFile)
	} else {
		fmt.Fprintf(b, "환경 파일: %s\n", msgNone)
	}
	if len(res.Target.Profiles) > 0 {
		fmt.Fprintf(b, "프로필: %s\n", strings.Join(res.Target.Profiles, ", "))
	} else {
		fmt.Fprintf(b, "프로필: %s\n", msgNone)
	}
	if snap.Environment != "" {
		fmt.Fprintf(b, "실행환경: %s\n", snap.Environment)
	}
	if tools := toolLine(res.Tools); tools != "" {
		fmt.Fprintf(b, "%s\n", tools)
	}
}

func toolLine(t core.ToolInfo) string {
	var parts []string
	if t.DockerVersion != "" {
		parts = append(parts, "Docker "+t.DockerVersion)
	}
	if t.ComposeVersion != "" {
		parts = append(parts, "Compose "+t.ComposeVersion)
	}
	if t.EndpointKind != "" {
		parts = append(parts, msgLocalConnection)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func writeSummary(b *strings.Builder, snap stack.Snapshot) {
	s := snap.Summary
	parts := []string{
		fmt.Sprintf("서비스 %d개", s.Services),
		fmt.Sprintf("실행 중 %d개", s.Running),
		fmt.Sprintf("인스턴스 %d개", s.Instances),
	}
	if s.Healthy > 0 {
		parts = append(parts, fmt.Sprintf("healthy %d개", s.Healthy))
	}
	if s.Unverified > 0 {
		parts = append(parts, fmt.Sprintf("앱 확인 안 됨 %d개", s.Unverified))
	}
	if s.Unhealthy > 0 {
		parts = append(parts, fmt.Sprintf("unhealthy %d개", s.Unhealthy))
	}
	if s.Stopped > 0 {
		parts = append(parts, fmt.Sprintf("중지 %d개", s.Stopped))
	}
	if s.NotCreated > 0 {
		parts = append(parts, fmt.Sprintf("컨테이너 없음 %d개", s.NotCreated))
	}
	fmt.Fprintf(b, "%s\n", strings.Join(parts, " · "))
}

func writeService(b *strings.Builder, svc stack.Service) {
	fmt.Fprintf(b, "%s\n", svc.Name)
	state := stateLabel(svc.State)
	health := healthLabel(svc)
	fmt.Fprintf(b, "  상태: %s", state)
	if health != "" {
		fmt.Fprintf(b, " · %s", health)
	}
	b.WriteString("\n")
	if !svc.Configured {
		fmt.Fprintf(b, "  설정: %s\n", msgUnconfigured)
	}
	switch {
	case svc.Build && svc.Image != "":
		fmt.Fprintf(b, "  이미지: %s %s\n", svc.Image, msgLocalBuild)
	case svc.Build:
		fmt.Fprintf(b, "  이미지: %s\n", msgLocalBuild)
	case svc.Image != "":
		fmt.Fprintf(b, "  이미지: %s\n", svc.Image)
	}
	if len(svc.Instances) == 0 {
		fmt.Fprintf(b, "  %s\n", msgNoInstances)
	} else {
		fmt.Fprintf(b, "  인스턴스 %d개\n", len(svc.Instances))
		for _, in := range svc.Instances {
			fmt.Fprintf(b, "    - %s%s\n", in.Name, instanceDetail(in))
		}
	}
	if len(svc.Connections) == 0 {
		fmt.Fprintf(b, "  연결: %s\n", msgNone)
	} else {
		fmt.Fprintf(b, "  연결\n")
		for _, c := range svc.Connections {
			fmt.Fprintf(b, "    - %s\n", connectionLabel(c))
		}
	}
}

func instanceDetail(in stack.Instance) string {
	parts := []string{in.State}
	if in.Health != stack.HealthNone {
		parts = append(parts, string(in.Health))
	}
	if in.State == string(stack.StateExited) {
		parts = append(parts, fmt.Sprintf("종료 코드 %d", in.ExitCode))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func connectionLabel(c stack.Connection) string {
	host := c.HostIP
	if host == "" {
		host = msgAllInterfaces
	}
	published := fmt.Sprintf("%d", c.PublishedPort)
	if c.PublishedPort == 0 {
		published = msgDynamicPort
	}
	label := fmt.Sprintf("%s:%s → %d/%s", host, published, c.TargetPort, c.Protocol)
	if c.Source == stack.SourceConfig {
		label += msgConfiguredPort
	}
	return label
}

func healthLabel(svc stack.Service) string {
	switch {
	case svc.State == stack.StateRunning:
		switch svc.Health {
		case stack.HealthHealthy:
			if svc.AppVerified {
				return msgHealthyVerified
			}
			return msgHealthyUnverified
		case stack.HealthStarting:
			return msgHealthStarting
		case stack.HealthUnhealthy:
			return msgHealthUnhealthy
		default:
			if svc.HealthcheckConfigured {
				return msgHealthMissing
			}
			return msgNoHealthcheck
		}
	case svc.HealthcheckConfigured:
		return msgHealthcheckConfigured
	default:
		return ""
	}
}

func stateLabel(state stack.ServiceState) string {
	switch state {
	case stack.StateRunning:
		return "실행 중"
	case stack.StateExited:
		return "중지됨"
	case stack.StatePaused:
		return "일시정지됨"
	case stack.StateRestarting:
		return "재시작 중"
	case stack.StateCreated:
		return "생성됨"
	case stack.StateNotCreated:
		return "컨테이너 없음"
	default:
		if state == "" {
			return "상태 알 수 없음"
		}
		return string(state)
	}
}

func writeGuidance(b *strings.Builder, width int) {
	fmt.Fprintf(b, "다음 행동\n")
	for _, line := range []string{
		msgGuideRefreshInteractive,
		msgGuidePending,
		msgGuideSecrets,
	} {
		writeWrappedBullet(b, line, width)
	}
}

func writeWrappedBullet(b *strings.Builder, text string, width int) {
	const indent = "  - "
	const continuation = "    "
	if width <= len(indent)+8 {
		fmt.Fprintf(b, "%s%s\n", indent, text)
		return
	}
	limit := width - len(indent)
	words := strings.Fields(text)
	if len(words) == 0 {
		fmt.Fprintf(b, "%s\n", indent)
		return
	}
	line := words[0]
	first := true
	flush := func() {
		if first {
			fmt.Fprintf(b, "%s%s\n", indent, line)
			first = false
		} else {
			fmt.Fprintf(b, "%s%s\n", continuation, line)
		}
	}
	for _, w := range words[1:] {
		if len([]rune(line))+1+len([]rune(w)) > limit {
			flush()
			line = w
			continue
		}
		line += " " + w
	}
	flush()
}
