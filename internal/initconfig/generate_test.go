package initconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dockerfileProject(t *testing.T) string {
	t.Helper()
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "Dockerfile"), "FROM busybox:1.36\nCMD [\"httpd\", \"-f\", \"-p\", \"8080\"]\n")
	return project
}

func defaultDestination(t *testing.T, project string) Destination {
	t.Helper()
	dest, err := destinationPath(project, "")
	if err != nil {
		t.Fatalf("destination: %v", err)
	}
	return dest
}

func TestBuildDockerfileModeUsesOnlyExplicitInputs(t *testing.T) {
	project := dockerfileProject(t)
	dest := defaultDestination(t, project)
	spec, err := buildSpec(project, Input{
		Mode:       ModeDockerfile,
		Service:    "app",
		Context:    ".",
		Dockerfile: "Dockerfile",
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `services:
  app:
    build:
      context: .
      dockerfile: Dockerfile
`
	if spec.Content != want {
		t.Fatalf("content:\n%s\nwant:\n%s", spec.Content, want)
	}
	if strings.Contains(spec.Content, "ports:") || strings.Contains(spec.Content, "volumes:") {
		t.Fatalf("content inferred ports or volumes:\n%s", spec.Content)
	}
	found := false
	for _, notice := range spec.Notices {
		if strings.Contains(notice, "EXPOSE") {
			found = true
		}
	}
	if !found {
		t.Fatalf("EXPOSE guidance missing from notices: %v", spec.Notices)
	}
}

func TestBuildImageModeWritesUserRuntimeFieldsAndDeclaresNamedVolumes(t *testing.T) {
	project := t.TempDir()
	dest := defaultDestination(t, project)
	spec, err := buildSpec(project, Input{
		Mode:    ModeImage,
		Service: "web",
		Image:   "nginx:1.27-alpine",
		Ports:   []string{"8080:80"},
		Volumes: []string{"./data:/data", "cache:/cache:ro"},
		Env:     []string{"FOO=bar"},
		Command: "httpd -f -p 8080",
		Restart: "unless-stopped",
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `services:
  web:
    image: nginx:1.27-alpine
    ports:
      - 8080:80
    volumes:
      - ./data:/data
      - cache:/cache:ro
    environment:
      FOO: bar
    command: httpd -f -p 8080
    restart: unless-stopped
volumes:
  cache: {}
`
	if spec.Content != want {
		t.Fatalf("content:\n%s\nwant:\n%s", spec.Content, want)
	}
	if len(spec.NamedVolumes) != 1 || spec.NamedVolumes[0] != "cache" {
		t.Fatalf("named volumes = %v", spec.NamedVolumes)
	}
	if len(spec.Binds) != 1 || !strings.HasSuffix(spec.Binds[0].HostPath, "/data") {
		t.Fatalf("binds = %+v", spec.Binds)
	}
}

func TestBuildImageModeWithoutOptionalFieldsAddsNothing(t *testing.T) {
	project := t.TempDir()
	dest := defaultDestination(t, project)
	spec, err := buildSpec(project, Input{
		Mode: ModeImage, Service: "web", Image: "busybox:1.36",
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := `services:
  web:
    image: busybox:1.36
`
	if spec.Content != want {
		t.Fatalf("content:\n%s\nwant:\n%s", spec.Content, want)
	}
}

func TestBuildDockerfileInSubfolderKeepsComposePathSemantics(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "app", "Dockerfile"), "FROM busybox:1.36\n")
	dest := defaultDestination(t, project)
	spec, err := buildSpec(project, Input{
		Mode: ModeDockerfile, Service: "app", Context: "./app", Dockerfile: "Dockerfile",
		Volumes: []string{"./app/data:/data"},
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{"context: ./app", "dockerfile: Dockerfile", "- ./app/data:/data"} {
		if !strings.Contains(spec.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, spec.Content)
		}
	}
}

func TestBuildRelativePathsFollowTheDestinationDirectory(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "Dockerfile"), "FROM busybox:1.36\n")
	gen := filepath.Join(project, "gen")
	if err := os.Mkdir(gen, 0o755); err != nil {
		t.Fatal(err)
	}
	dest, err := destinationPath(project, "gen/compose.yaml")
	if err != nil {
		t.Fatalf("destination: %v", err)
	}
	spec, err := buildSpec(project, Input{
		Mode: ModeDockerfile, Service: "app", Context: ".", Dockerfile: "Dockerfile",
		Volumes: []string{"./data:/data"},
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{"context: ..", "dockerfile: Dockerfile", "- ../data:/data"} {
		if !strings.Contains(spec.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, spec.Content)
		}
	}
}

func TestBuildRejectsMissingDockerfileAndInvalidContext(t *testing.T) {
	project := t.TempDir()
	dest := defaultDestination(t, project)
	_, err := buildSpec(project, Input{
		Mode: ModeDockerfile, Service: "app", Context: ".", Dockerfile: "Dockerfile",
	}, dest)
	assertInputError(t, err, "Dockerfile")

	if err := os.Mkdir(filepath.Join(project, "ctx"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = buildSpec(project, Input{
		Mode: ModeDockerfile, Service: "app", Context: "./missing", Dockerfile: "Dockerfile",
	}, dest)
	assertInputError(t, err, "컨텍스트")
}

func TestBuildNoticesPlaintextEnvironmentAndOutsideDestination(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	dest, err := destinationPath(project, filepath.Join(outside, "compose.yaml"))
	if err != nil {
		t.Fatalf("destination: %v", err)
	}
	if !dest.Outside {
		t.Fatal("explicit outside destination was not detected")
	}
	spec, err := buildSpec(project, Input{
		Mode: ModeImage, Service: "web", Image: "busybox:1.36", Env: []string{"FOO=bar"},
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	joined := strings.Join(spec.Notices, "\n")
	for _, want := range []string{"평문", "프로젝트 폴더 밖"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("notices missing %q:\n%s", want, joined)
		}
	}
}

func TestBuildDockerfilePathOutsideContextIsRelativeToTheContext(t *testing.T) {
	project := t.TempDir()
	writeTestFile(t, filepath.Join(project, "Dockerfile"), "FROM busybox:1.36\n")
	if err := os.Mkdir(filepath.Join(project, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := defaultDestination(t, project)
	spec, err := buildSpec(project, Input{
		Mode: ModeDockerfile, Service: "app", Context: "./app", Dockerfile: "../Dockerfile",
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(spec.Content, "dockerfile: ../Dockerfile") {
		t.Fatalf("content:\n%s", spec.Content)
	}
}

func TestBuildListsAnAnonymousVolumeAsAResource(t *testing.T) {
	project := t.TempDir()
	dest := defaultDestination(t, project)
	spec, err := buildSpec(project, Input{
		Mode: ModeImage, Service: "web", Image: "busybox:1.36", Volumes: []string{"/data"},
	}, dest)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	found := false
	for _, resource := range spec.Resources {
		if resource.Kind == "anonymous_volume" && resource.Name == "/data" {
			found = true
		}
	}
	if !found {
		t.Fatalf("anonymous volume resource missing: %+v", spec.Resources)
	}
}
