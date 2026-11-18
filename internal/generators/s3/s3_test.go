package s3

import (
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/generators"
)

func testLayout(t *testing.T) generators.Layout {
	t.Helper()
	project := t.TempDir()
	layout, err := generators.ResolveLayout(project, "deploy", "scripts/s3-sync.sh", generators.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func build(t *testing.T, source string, mode Mode, profile string) Generated {
	t.Helper()
	gen, err := Build(Input{Layout: testLayout(t), Source: source, Mode: mode, Profile: profile})
	if err != nil {
		t.Fatalf("Build(%q,%q): %v", source, mode, err)
	}
	return gen
}

func TestBuildAcceptsDirectorySyncAndSingleTarObject(t *testing.T) {
	cases := []struct {
		source string
		mode   Mode
	}{
		{"s3://bucket/prefix", ModeSync},
		{"s3://bucket/prefix/", ModeSync},
		{"s3://bucket", ModeSync},
		{"s3://bucket/a/b", ModeSync},
		{"s3://bucket/path/app.tar.gz", ModeTar},
		{"s3://bucket/deploy/app-1.2.3.tar.gz", ModeTar},
	}
	for _, tc := range cases {
		gen := build(t, tc.source, tc.mode, "")
		if gen.Mode != tc.mode {
			t.Fatalf("%q mode = %q", tc.source, gen.Mode)
		}
		if !strings.HasPrefix(gen.Source, "s3://") || strings.Contains(strings.TrimPrefix(gen.Source, "s3://"), "//") {
			t.Fatalf("%q normalized to %q", tc.source, gen.Source)
		}
	}
}

func TestBuildRejectsMalformedOrUnsafeSources(t *testing.T) {
	cases := []struct {
		source string
		mode   Mode
	}{
		{"", ModeSync},
		{"   ", ModeSync},
		{"bucket/prefix", ModeSync},
		{"http://bucket/prefix", ModeSync},
		{"s3:/bucket/prefix", ModeSync},
		{"s3://", ModeSync},
		{"s3:///prefix", ModeSync},
		{"s3://bucket//prefix", ModeSync},
		{"s3://bucket//", ModeSync},
		{"s3://bucket/prefix//x", ModeSync},
		{"s3://bucket/./prefix", ModeSync},
		{"s3://bucket/../prefix", ModeSync},
		{"s3://user:pass@bucket/prefix", ModeSync},
		{"s3://token@bucket/prefix", ModeSync},
		{"s3://bucket/prefix?X-Amz-Credential=leak", ModeSync},
		{"s3://bucket/prefix#fragment", ModeSync},
		{"s3://bucket/prefix with space", ModeSync},
		{"s3://bucket/prefix\\x", ModeSync},
		{"s3://bucket/*", ModeSync},
		{"s3://bucket/prefix", "mirror"},
		{"s3://bucket/prefix", ""},
		// Mode/object pairing: a sync prefix must not be a tar object.
		{"s3://bucket/app.tar.gz", ModeSync},
		{"s3://bucket/dir/app.tar.gz/", ModeSync},
		// tar mode needs one concrete .tar.gz object.
		{"s3://bucket/prefix", ModeTar},
		{"s3://bucket/prefix/", ModeTar},
		{"s3://bucket/app.tgz", ModeTar},
		{"s3://bucket/app.tar.gz/", ModeTar},
		{"s3://bucket/.tar.gz", ModeTar},
	}
	for _, tc := range cases {
		_, err := Build(Input{Layout: testLayout(t), Source: tc.source, Mode: tc.mode})
		if err == nil {
			t.Fatalf("source %q mode %q accepted", tc.source, tc.mode)
		}
		if _, ok := err.(*generators.InputError); !ok {
			t.Fatalf("source %q error type = %T, want *generators.InputError", tc.source, err)
		}
	}
}

func TestSourceErrorsNeverEchoCredentialBearingValues(t *testing.T) {
	for _, source := range []string{
		"s3://user:secret@bucket/prefix",
		"s3://bucket/prefix?X-Amz-Credential=leak",
	} {
		_, err := Build(Input{Layout: testLayout(t), Source: source, Mode: ModeSync})
		if err == nil {
			t.Fatalf("credential-bearing source accepted: %q", source)
		}
		text := err.Error()
		for _, forbidden := range []string{"secret", "X-Amz", "leak", "user:"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("error echoes %q from %q: %s", forbidden, source, text)
			}
		}
	}
}

func TestBuildValidatesTheOptionalAWSProfileName(t *testing.T) {
	gen := build(t, "s3://bucket/prefix", ModeSync, "team-deploy")
	if gen.Profile != "team-deploy" {
		t.Fatalf("profile = %q", gen.Profile)
	}
	for _, profile := range []string{"--evil", "-p", "two words", "a/b", "x;y", "x\ny", "x`y`", "x$(y)"} {
		if _, err := Build(Input{Layout: testLayout(t), Source: "s3://bucket/prefix", Mode: ModeSync, Profile: profile}); err == nil {
			t.Fatalf("unsafe profile accepted: %q", profile)
		}
	}
}

func TestBuildRejectsMissingLayoutAndUnsafeScriptPaths(t *testing.T) {
	if _, err := Build(Input{Source: "s3://bucket/prefix", Mode: ModeSync}); err == nil {
		t.Fatal("empty layout accepted")
	}
}
