package composeinstall

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/rootmyself/dcraft/internal/core"
)

func machoHeader(cputype uint32) []byte {
	b := make([]byte, 32)
	copy(b, machoMagic64)
	binary.LittleEndian.PutUint32(b[4:8], cputype)
	binary.LittleEndian.PutUint32(b[12:16], machoFileExecute)
	return b
}

func elfHeader(machine uint16) []byte {
	b := make([]byte, 32)
	copy(b, elfMagic)
	b[4] = elfClass64
	b[5] = elfDataLE
	binary.LittleEndian.PutUint16(b[16:18], elfTypeExec)
	binary.LittleEndian.PutUint16(b[18:20], machine)
	return b
}

func writeArtifact(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker-compose")
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateExecutableAcceptsMatchingPlatform(t *testing.T) {
	cases := []struct {
		platform Platform
		data     []byte
	}{
		{Platform{"darwin", "arm64"}, machoHeader(machoCpuArm64)},
		{Platform{"darwin", "amd64"}, machoHeader(machoCpuX86_64)},
		{Platform{"linux", "amd64"}, elfHeader(elfMachineX86_64)},
		{Platform{"linux", "arm64"}, elfHeader(elfMachineAArch64)},
	}
	for _, tc := range cases {
		path := writeArtifact(t, tc.data)
		if err := validateExecutable(path, tc.platform); err != nil {
			t.Fatalf("%v rejected a matching artifact: %v", tc.platform, err)
		}
	}
}

func TestValidateExecutableDistinguishesWrongArchitecture(t *testing.T) {
	cases := []struct {
		platform Platform
		data     []byte
	}{
		{Platform{"darwin", "arm64"}, machoHeader(machoCpuX86_64)},
		{Platform{"darwin", "amd64"}, machoHeader(machoCpuArm64)},
		{Platform{"linux", "amd64"}, elfHeader(elfMachineAArch64)},
		{Platform{"linux", "arm64"}, elfHeader(elfMachineX86_64)},
	}
	for _, tc := range cases {
		path := writeArtifact(t, tc.data)
		ce := asCoreError(t, validateExecutable(path, tc.platform))
		if ce.Diagnostics[0].Category != core.CategoryComposeArchMismatch {
			t.Fatalf("%v category = %q", tc.platform, ce.Diagnostics[0].Category)
		}
	}
}

func TestValidateExecutableRejectsNonExecutableArtifacts(t *testing.T) {
	cases := map[string][]byte{
		"html error page": []byte("<html>rate limited</html>"),
		"shell script":    []byte("#!/bin/sh\necho hi\n"),
		"empty":           {},
		"truncated elf":   {0x7f, 'E', 'L', 'F'},
		"wrong os":        elfHeader(elfMachineAArch64),
	}
	for name, data := range cases {
		p := Platform{"darwin", "arm64"}
		if name == "wrong os" {
			p = Platform{"darwin", "arm64"}
		}
		path := writeArtifact(t, data)
		ce := asCoreError(t, validateExecutable(path, p))
		if ce.Diagnostics[0].Category != core.CategoryComposeArtifactInvalid {
			t.Fatalf("%s category = %q", name, ce.Diagnostics[0].Category)
		}
	}
}

func TestValidateExecutableRejectsMachOWithWrongFileType(t *testing.T) {
	data := machoHeader(machoCpuArm64)
	binary.LittleEndian.PutUint32(data[12:16], 6) // MH_DYLIB
	ce := asCoreError(t, validateExecutable(writeArtifact(t, data), Platform{"darwin", "arm64"}))
	if ce.Diagnostics[0].Category != core.CategoryComposeArtifactInvalid {
		t.Fatalf("category = %q", ce.Diagnostics[0].Category)
	}
}
