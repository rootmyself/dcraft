package initconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

// A folder that already has Compose input is not a blank slate, so the flow
// says so before its first question instead of refusing at save time.
func TestWizardStepsStateAnExistingConfigurationFirst(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: nginx\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := New(&fakeValidator{}, &fakeSaver{})
	spec, err := f.WizardSteps(context.Background(), core.Invocation{
		Target: core.Target{ProjectDir: dir, Files: []string{dir + "/compose.yaml"}},
	})
	if err != nil {
		t.Fatalf("WizardSteps: %v", err)
	}
	if !strings.Contains(spec.Notice, "이미 Compose 설정이 있음") {
		t.Fatalf("notice = %q, want the existing-file warning", spec.Notice)
	}
	if !strings.Contains(spec.Notice, "compose.yaml") {
		t.Fatalf("notice does not name the file: %q", spec.Notice)
	}
}

// A running stack is a separate fact and is stated too.
func TestWizardStepsStateARunningStack(t *testing.T) {
	dir := t.TempDir()
	f := New(&fakeValidator{}, &fakeSaver{})
	spec, err := f.WizardSteps(context.Background(), core.Invocation{
		Target:  core.Target{ProjectDir: dir},
		Options: map[string]string{"stack-running": "true"},
	})
	if err != nil {
		t.Fatalf("WizardSteps: %v", err)
	}
	if !strings.Contains(spec.Notice, "이미 실행 중") {
		t.Fatalf("notice = %q, want the running-stack warning", spec.Notice)
	}
}

// A blank folder says nothing.
func TestWizardStepsStayQuietOnABlankFolder(t *testing.T) {
	f := New(&fakeValidator{}, &fakeSaver{})
	spec, err := f.WizardSteps(context.Background(), core.Invocation{
		Target: core.Target{ProjectDir: t.TempDir()},
	})
	if err != nil {
		t.Fatalf("WizardSteps: %v", err)
	}
	if spec.Notice != "" {
		t.Fatalf("a blank folder produced a notice: %q", spec.Notice)
	}
}
