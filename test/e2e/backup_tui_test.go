package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/backup"
)

func TestBackupTUIWizardSelectsVolumeAndShowsTheEffectBeforeRunning(t *testing.T) {
	fake := backupFake(t)
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())

	// The menu order is the registration order; compute the distance from
	// status so another ticket's feature does not break this journey.
	steps := []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: strings.Repeat("\x1b[B", menuDownPresses(t, "backup"))},
		{wait: "> 데이터 백업 (backup)", send: "\r"},
		{wait: "데이터 백업 설정", send: "\r"}, // choose the first named volume
		{wait: "백업 저장 폴더", send: "\r"},  // accept the shown default path
		{wait: "백업 계획", send: ""},       // effect preview, nothing has run
		{wait: "영향 서비스: app, worker", send: ""},
		{wait: "저장 폴더: ./dcraft-backups", send: "\r"},
		{wait: "백업 완료", send: ""},
	}
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	artifacts, err := backup.List(filepath.Join(project, "dcraft-backups"))
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(artifacts) != 1 {
		t.Fatalf("TUI backup artifacts = %d\n%s", len(artifacts), output)
	}
	if _, err := backup.Verify(artifacts[0].Path); err != nil {
		t.Fatalf("TUI backup did not verify: %v", err)
	}
	log := strings.Join(fake.calls(t), "\n")
	for _, want := range []string{" stop app", "create --name dcraft-backup-", "start -a dcraft-backup-", "rm -f dcraft-backup-", " start app"} {
		if !strings.Contains(log, want) {
			t.Fatalf("TUI did not use the shared backup path (%q missing):\n%s", want, log)
		}
	}
}

func TestBackupTUIWizardSelectsAMixOfVolumeAndBindFolder(t *testing.T) {
	hostDir := writeBindFixture(t)
	fake := backupFake(t)
	fake.writeConfig(t, backupBindConfig(hostDir))
	project := t.TempDir()
	writeProject(t, project)
	env := fake.env(t, t.TempDir())
	resolved, err := filepath.EvalSymlinks(hostDir)
	if err != nil {
		t.Fatal(err)
	}

	// The menu order is the registration order; compute the distance from
	// status so another ticket's feature does not break this journey.
	steps := []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: strings.Repeat("\x1b[B", menuDownPresses(t, "backup"))},
		{wait: "> 데이터 백업 (backup)", send: "\r"},
		{wait: "백업할 데이터 선택", send: " "}, // check the first volume
		{wait: "> [x] 볼륨 data", send: "down"},
		{wait: "> [ ] 폴더 ", send: " "}, // add the bind folder
		{wait: "[x] 폴더 ", send: "\r"},
		{wait: "백업 저장 폴더", send: "\r"},
		{wait: "백업 계획", send: ""},
		{wait: "다른 스택 공유: 없음", send: "\r"},
		{wait: "백업 완료", send: ""},
	}
	output, code := runLifecyclePTYSteps(t, project, env, steps)
	if code != 0 {
		t.Fatalf("TUI exit = %d\n%s", code, output)
	}
	artifacts, err := backup.List(filepath.Join(project, "dcraft-backups"))
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("TUI mixed backup artifacts = %d\n%s", len(artifacts), output)
	}
	kinds := map[backup.Kind]int{}
	for _, artifact := range artifacts {
		kinds[artifact.Manifest.Source.Kind]++
		if artifact.Manifest.Source.Kind == backup.KindBind && artifact.Manifest.Source.HostPath != resolved {
			t.Fatalf("bind host path = %q, want %q", artifact.Manifest.Source.HostPath, resolved)
		}
	}
	if kinds[backup.KindVolume] != 1 || kinds[backup.KindBind] != 1 {
		t.Fatalf("artifact kinds = %v", kinds)
	}
	for _, artifact := range artifacts {
		if _, err := backup.Verify(artifact.Path); err != nil {
			t.Fatalf("TUI backup did not verify: %v", err)
		}
	}
	log := strings.Join(fake.calls(t), "\n")
	if !strings.Contains(log, "create --name dcraft-backup-") {
		t.Fatalf("the volume half did not use the helper:\n%s", log)
	}
	if !strings.Contains(log, " stop app") || !strings.Contains(log, " start app") {
		t.Fatalf("TUI did not stop and restart the bind consumer:\n%s", log)
	}
}
