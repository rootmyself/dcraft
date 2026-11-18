package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rootmyself/dcraft/internal/buildinfo"
)

func TestVersionFlagPrintsIdentificationWithoutBuildingTheApp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"--version"}, strings.NewReader(""), &stdout, &stderr, func(string) string {
		t.Fatal("version must be answered before the application initializes")
		return ""
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got, want := stdout.String(), buildinfo.Current().String()+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestVersionFlagIsOnlyRecognizedAlone(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{args: nil, want: false},
		{args: []string{"--version"}, want: true},
		{args: []string{"-version"}, want: true},
		{args: []string{"--help"}, want: false},
		{args: []string{"status"}, want: false},
		{args: []string{"--version", "--json"}, want: false},
		{args: []string{"status", "--version"}, want: false},
	}
	for _, tc := range cases {
		if got := versionRequested(tc.args); got != tc.want {
			t.Fatalf("versionRequested(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
