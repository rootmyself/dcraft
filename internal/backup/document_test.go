package backup

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
	"github.com/rootmyself/dcraft/internal/data"
)

// backupResult is one published volume archive.
func backupResult() core.Result {
	return core.Result{Data: Payload{Backup: Report{
		ProjectName:      "acme",
		OutputDir:        "/work/acme/dcraft-backups",
		AffectedServices: []string{"api"},
		Stopped:          []string{"api"},
		Restarted:        []string{"api"},
		Sources: []Source{
			{Kind: data.KindVolume, VolumeKey: "data", VolumeName: "acme_data"},
		},
		Artifacts: []ArtifactReport{{
			Source: Source{Kind: data.KindVolume, VolumeKey: "data"}, Path: "/work/acme/dcraft-backups/one",
			ArtifactSizeBytes: 2048, Entries: 12, Complete: true,
		}},
		Notices: []string{
			msgSensitiveNotice, msgHelperImageNotice, msgExcludedNotice,
			msgNoAutoDeleteNote, msgStoppedOnlyOurs,
		},
	}}}
}

// The interactive document reports the archive and keeps only the statements
// that change a later decision: the data may hold credentials, ownership limits
// do not apply to a pure volume backup, and dcraft never deletes old backups.
func TestDocumentKeepsOnlyDecisionChangingStatements(t *testing.T) {
	doc := Document(backupResult())
	if len(doc.Blocks) < 3 {
		t.Fatalf("blocks = %v", doc.Blocks)
	}
	var callout string
	for _, b := range doc.Blocks {
		if b.Kind == core.BlockCallout {
			callout += b.Text + "\n"
		}
	}
	if !strings.Contains(callout, "비밀번호") {
		t.Fatalf("sensitive-data risk missing:\n%s", callout)
	}
	if !strings.Contains(callout, "자동으로 삭제하지 않음") {
		t.Fatalf("backup retention statement missing:\n%s", callout)
	}
	for _, dropped := range []string{"Docker 컨테이너(tar)로 수행", "백업하지 않음", "이미 중지된 서비스"} {
		if strings.Contains(callout, dropped) {
			t.Fatalf("run-description notice survived: %q\n%s", dropped, callout)
		}
	}
}

// A host-folder source adds the ownership limit; a volume-only backup does not
// claim one.
func TestDocumentHostFolderAddsItsOwnLimit(t *testing.T) {
	res := backupResult()
	payload := res.Data.(Payload)
	payload.Backup.Sources = append(payload.Backup.Sources, Source{Kind: data.KindBind, HostPath: "/srv/data"})
	res.Data = payload

	joined := ""
	for _, b := range Document(res).Blocks {
		if b.Kind == core.BlockCallout {
			joined += b.Text
		}
	}
	if !strings.Contains(joined, "호스트 사용자 권한") {
		t.Fatalf("host-folder limit missing:\n%s", joined)
	}

	volumeOnly := ""
	for _, b := range Document(backupResult()).Blocks {
		if b.Kind == core.BlockCallout {
			volumeOnly += b.Text
		}
	}
	if strings.Contains(volumeOnly, "호스트 사용자 권한") {
		t.Fatalf("a volume-only backup claims a host-folder limit:\n%s", volumeOnly)
	}
}

// The CLI presenter keeps its full notice list, so a script still sees every
// fact the run produced.
func TestPresentKeepsEveryNotice(t *testing.T) {
	out := Present(backupResult(), 0)
	for _, want := range []string{"산출물:", "안내:", msgSensitiveNotice, msgHelperImageNotice} {
		if !strings.Contains(out, want) {
			t.Fatalf("CLI output lost %q:\n%s", want, out)
		}
	}
}
