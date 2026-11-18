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

	output, code := runLifecyclePTYSteps(t, project, env, []lifecyclePTYStep{
		{wait: "> 스택 상태 (status)", send: "up"},
		{wait: "> 비밀값 연결 (secrets)", send: "up"},
		{wait: "> 이전 버전 복원 (rollback)", send: "up"},
		{wait: "> 데이터 복원 (restore)", send: "up"},
		{wait: "> 스택 재시작 (restart)", send: "up"},
		{wait: "> 로그 보기 (logs)", send: "up"},
		{wait: "> Compose 설치 (install-compose)", send: "up"},
		{wait: "> 호스트 스크립트 (hooks)", send: "up"},
		{wait: "> 서비스 접속 (exec)", send: "up"},
		{wait: "> 스택 제거 (down)", send: "up"},
		{wait: "> 데이터 백업 (backup)", send: "\r"},
		{wait: "데이터 백업 설정", send: "\r"}, // choose the first named volume
		{wait: "백업 저장 폴더", send: "\r"},  // accept the shown default path
		{wait: "백업 계획", send: ""},       // effect preview, nothing has run
		{wait: "영향 서비스: app, worker", send: ""},
		{wait: "저장 폴더: ./dcraft-backups", send: "\r"},
		{wait: "백업 완료", send: ""},
	})
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
