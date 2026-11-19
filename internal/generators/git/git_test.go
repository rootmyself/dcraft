package git

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/generators"
)

func testLayout(t *testing.T) generators.Layout {
	t.Helper()
	project := t.TempDir()
	layout, err := generators.ResolveLayout(project, "src", "scripts/git-sync.sh")
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func build(t *testing.T, repo, ref, stage string) Generated {
	t.Helper()
	gen, err := Build(Input{
		Layout:     testLayout(t),
		Repository: repo,
		Ref:        ref,
		Stage:      stage,
	})
	if err != nil {
		t.Fatalf("Build(%q,%q): %v", repo, ref, err)
	}
	return gen
}

func TestClassifyRefUsesFullRefsAndFullSHAOnly(t *testing.T) {
	cases := []struct {
		ref  string
		kind Kind
		want string
	}{
		{"main", KindAuto, "main"},
		{"v1.0", KindAuto, "v1.0"},
		{"refs/heads/main", KindBranch, "main"},
		{"refs/tags/v1.0", KindTag, "v1.0"},
		{"b732a722865cb614d513176e36133caada71ea06", KindCommit, "b732a722865cb614d513176e36133caada71ea06"},
	}
	for _, tc := range cases {
		kind, short, err := ClassifyRef(tc.ref)
		if err != nil {
			t.Fatalf("ClassifyRef(%q): %v", tc.ref, err)
		}
		if kind != tc.kind || short != tc.want {
			t.Fatalf("ClassifyRef(%q) = %q,%q want %q,%q", tc.ref, kind, short, tc.kind, tc.want)
		}
	}
}

func TestClassifyRefRejectsUnsafeOrShortValues(t *testing.T) {
	for _, ref := range []string{"", " ", "-main", "main branch", "a..b", "a~1", "a^", "a:b", "refs/pull/1/head", "b732a72", "0123456789abcdef"} {
		if _, _, err := ClassifyRef(ref); err == nil {
			t.Fatalf("unsafe ref accepted: %q", ref)
		}
	}
}

func TestBuildRejectsCredentialsInTheRepositoryURL(t *testing.T) {
	for _, repo := range []string{
		"https://user:secret@example.com/repo.git",
		"https://token@example.com/repo.git",
		"http://user:pass@example.com/repo.git",
		"ssh://git:secret@example.com/repo.git",
	} {
		_, err := Build(Input{
			Layout: testLayout(t), Repository: repo, Ref: "main", Stage: "pre",
		})
		if err == nil {
			t.Fatalf("credential URL accepted: %q", repo)
		}
	}
	// SSH usernames are not credentials.
	build(t, "git@github.com:owner/repo.git", "main", "pre")
	build(t, "ssh://git@example.com/repo.git", "main", "pre")
}

func TestBuildRejectsUnknownStageAndMissingInputs(t *testing.T) {
	if _, err := Build(Input{Layout: testLayout(t), Repository: "https://example.com/r.git", Ref: "main", Stage: "later"}); err == nil {
		t.Fatal("unknown stage accepted")
	}
	if _, err := Build(Input{Layout: testLayout(t), Ref: "main", Stage: "pre"}); err == nil {
		t.Fatal("empty repository accepted")
	}
}

func TestAutoRefScriptDetectsBranchOrTagAndRefusesAmbiguity(t *testing.T) {
	script := string(build(t, "https://example.com/repo.git", "main", "pre").Content)
	for _, want := range []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"REF_KIND='auto'",
		"refs/heads/",
		"refs/tags/",
		"git ls-remote --exit-code --heads",
		"git ls-remote --exit-code --tags",
		"같은 이름의 branch와 tag",
		"FETCH_HEAD^{commit}",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("auto script missing %q:\n%s", want, script)
		}
	}
}

func TestTagScriptPeelsAnnotatedTags(t *testing.T) {
	gen := build(t, "https://example.com/repo.git", "refs/tags/v1.0", "pre")
	script := string(gen.Content)
	if gen.Kind != KindTag || gen.RefShort != "v1.0" {
		t.Fatalf("kind/ref = %q/%q", gen.Kind, gen.RefShort)
	}
	for _, want := range []string{"REF_KIND='tag'", `refs/tags/$REF`, "FETCH_HEAD^{commit}", "annotated", "태그"} {
		if !strings.Contains(script, want) {
			t.Fatalf("tag script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "--branch") {
		t.Fatalf("tag script relies on clone --branch:\n%s", script)
	}
}

func TestBranchScriptUsesTheBranchNamespace(t *testing.T) {
	gen := build(t, "https://example.com/repo.git", "refs/heads/main", "pre")
	script := string(gen.Content)
	if gen.Kind != KindBranch {
		t.Fatalf("kind = %q", gen.Kind)
	}
	for _, want := range []string{"REF_KIND='branch'", `refs/heads/$REF`, "fetch --quiet --depth 1 origin"} {
		if !strings.Contains(script, want) {
			t.Fatalf("branch script missing %q:\n%s", want, script)
		}
	}
}

func TestFullSHAScriptNeverUsesBranchOrShortSHA(t *testing.T) {
	sha := "b732a722865cb614d513176e36133caada71ea06"
	gen := build(t, "file:///tmp/repo.git", sha, "post")
	script := string(gen.Content)
	if gen.Kind != KindCommit {
		t.Fatalf("kind = %q", gen.Kind)
	}
	for _, want := range []string{"REF_KIND='commit'", sha, "짧은 SHA", `fetch --quiet --depth 1 origin "$REFSPEC"`, "사후(post)"} {
		if !strings.Contains(script, want) {
			t.Fatalf("SHA script missing %q:\n%s", want, script)
		}
	}
	if !strings.Contains(script, `commit) REFSPEC="$REF"`) {
		t.Fatalf("SHA script does not fetch the commit directly:\n%s", script)
	}
}

func TestScriptIsNonDestructiveAndContainsNoCredentials(t *testing.T) {
	script := string(build(t, "https://example.com/repo.git", "main", "pre").Content)
	for _, line := range strings.Split(script, "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		for _, forbidden := range []string{"reset --hard", "checkout -f", "checkout --force", "rm -rf", "--force", "clean -fdx", "git push"} {
			if strings.Contains(code, forbidden) {
				t.Fatalf("script executes destructive command %q: %s", forbidden, code)
			}
		}
	}
	for _, want := range []string{
		"status --porcelain",
		"커밋되지 않은 변경",
		"checkout --quiet --detach",
		"인증 정보가 없습니다",
		"script_trust",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing safety text %q:\n%s", want, script)
		}
	}
}

func TestGeneratedScriptsAreValidBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	for _, ref := range []string{"main", "refs/tags/v1.0", "b732a722865cb614d513176e36133caada71ea06"} {
		gen := build(t, "https://example.com/repo.git", ref, "pre")
		path := t.TempDir() + "/script.sh"
		if err := os.WriteFile(path, gen.Content, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(bash, "-n", path)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bash -n %s: %v\n%s", ref, err, out)
		}
	}
}

func TestGeneratedScriptIsExecutableAndHasKoreanComments(t *testing.T) {
	gen := build(t, "https://example.com/repo.git", "main", "pre")
	if len(gen.SHA256) != 64 {
		t.Fatalf("sha256 = %q", gen.SHA256)
	}
	script := string(gen.Content)
	for _, want := range []string{
		"# 입력", "# 실행 위치", "# 하는 일", "# 전제 조건", "# 실패 시", "# 수정 방법",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("Korean comment %q missing:\n%s", want, script)
		}
	}
}
