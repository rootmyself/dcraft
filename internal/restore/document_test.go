package restore

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

func restoreResult(kind data.Kind) core.Result {
	return core.Result{Data: Payload{Restore: Report{ExecutionReport: ExecutionReport{
		ProjectName:  "acme",
		TargetKind:   kind,
		VolumeKey:    "data",
		VolumeName:   "acme_data",
		HostPath:     "/srv/data",
		ArtifactPath: "/work/acme/dcraft-backups/one",
		SafetyBackup: "/work/acme/dcraft-safety/one",
		DataState:    DataReplaced,
		Warnings:     []string{"현재 데이터 크기 확인 실패: denied"},
		Notices:      []string{msgOverwrite, msgInterrupt, msgSafetyNotice},
	}}}}
}

// The interactive document reports the replaced data and keeps only the
// statements that change a later decision.
func TestDocumentKeepsOnlyDecisionChangingStatements(t *testing.T) {
	doc := Document(restoreResult(data.KindVolume))
	var callout string
	for _, b := range doc.Blocks {
		if b.Kind == core.BlockCallout {
			callout += b.Text + "\n"
		}
	}
	for _, want := range []string{"병합이 아님", "안전 백업", "자동으로 삭제하지 않음", "xattr"} {
		if !strings.Contains(callout, want) {
			t.Fatalf("statement %q missing:\n%s", want, callout)
		}
	}
	for _, dropped := range []string{"잠시 중지", "프로젝트 훅을 자동 실행하지 않음"} {
		if strings.Contains(callout, dropped) {
			t.Fatalf("run-description notice survived: %q\n%s", dropped, callout)
		}
	}
}

// A bind restore keeps its own ownership limit, which is broader than the
// volume one.
func TestDocumentHostFolderUsesItsOwnLimit(t *testing.T) {
	volume := ""
	for _, b := range Document(restoreResult(data.KindVolume)).Blocks {
		if b.Kind == core.BlockCallout {
			volume += b.Text
		}
	}
	bind := ""
	for _, b := range Document(restoreResult(data.KindBind)).Blocks {
		if b.Kind == core.BlockCallout {
			bind += b.Text
		}
	}
	if !strings.Contains(volume, "볼륨 복원은 uid/gid") {
		t.Fatalf("volume limit missing:\n%s", volume)
	}
	if !strings.Contains(bind, "연결 폴더 복원은 uid/gid") {
		t.Fatalf("bind limit missing:\n%s", bind)
	}
}

// Warnings from the run are shown by number, so a reader sees that something
// did not match the happy path.
func TestDocumentShowsRunWarnings(t *testing.T) {
	doc := Document(restoreResult(data.KindVolume))
	joined := ""
	for _, b := range doc.Blocks {
		if b.Kind == core.BlockCallout {
			joined += b.Text
		}
	}
	if !strings.Contains(joined, "현재 데이터 크기 확인 실패") {
		t.Fatalf("run warning missing:\n%s", joined)
	}
}

// The CLI presenter keeps its full notice and warning lists.
func TestPresentKeepsEveryNotice(t *testing.T) {
	out := Present(restoreResult(data.KindVolume), 0)
	for _, want := range []string{"안내:", msgInterrupt, msgOverwrite, "경고:", "현재 데이터 크기 확인 실패"} {
		if !strings.Contains(out, want) {
			t.Fatalf("CLI output lost %q:\n%s", want, out)
		}
	}
}
