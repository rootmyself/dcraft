package composeinstall

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rootmyself/dcraft/internal/core"
)

// Native executable header constants used only for validation. dcraft does not
// inspect or execute the artifact beyond this check and the docker CLI
// functional re-check.
var (
	machoMagic64 = []byte{0xcf, 0xfa, 0xed, 0xfe}
	machoMagic32 = []byte{0xce, 0xfa, 0xed, 0xfe}
	machoFat     = []byte{0xca, 0xfe, 0xba, 0xbe}
	elfMagic     = []byte{0x7f, 'E', 'L', 'F'}
)

const (
	machoCpuArch64   = 0x01000000
	machoCpuArm64    = machoCpuArch64 | 12
	machoCpuX86_64   = machoCpuArch64 | 7
	machoFileExecute = 2

	elfClass64        = 2
	elfDataLE         = 1
	elfDataBE         = 2
	elfTypeExec       = 2
	elfTypeDyn        = 3
	elfMachineX86_64  = 62
	elfMachineAArch64 = 183
)

// validateExecutable verifies that the downloaded artifact is a native
// executable for the current platform and architecture. It catches HTML error
// pages, scripts, truncated files and wrong-architecture downloads before
// anything is installed.
func validateExecutable(path string, p Platform) error {
	f, err := os.Open(path)
	if err != nil {
		return artifactError(err, "cannot open downloaded artifact")
	}
	defer func() { _ = f.Close() }()
	header := make([]byte, 64)
	n, readErr := io.ReadFull(f, header)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
		return artifactError(readErr, "cannot read downloaded artifact")
	}
	header = header[:n]
	if len(header) < 8 {
		return artifactError(nil, "downloaded artifact is too small to be an executable")
	}

	switch {
	case hasPrefix(header, machoMagic64) || hasPrefix(header, machoMagic32):
		if p.GOOS != "darwin" {
			return artifactError(nil, "Mach-O artifact does not match "+p.GOOS)
		}
		return validateMachO(header, p)
	case hasPrefix(header, elfMagic):
		if p.GOOS != "linux" {
			return artifactError(nil, "ELF artifact does not match "+p.GOOS)
		}
		return validateELF(header, p)
	case hasPrefix(header, machoFat):
		return artifactError(nil, "universal Mach-O artifacts are not supported")
	default:
		return artifactError(nil, fmt.Sprintf("unexpected artifact header %x", header[:min(len(header), 4)]))
	}
}

func validateMachO(header []byte, p Platform) error {
	if len(header) < 16 {
		return artifactError(nil, "truncated Mach-O header")
	}
	cputype := binary.LittleEndian.Uint32(header[4:8])
	filetype := binary.LittleEndian.Uint32(header[12:16])
	if filetype != machoFileExecute {
		return artifactError(nil, fmt.Sprintf("Mach-O file type %d is not an executable", filetype))
	}
	want := uint32(machoCpuX86_64)
	if p.GOARCH == "arm64" {
		want = machoCpuArm64
	}
	if cputype != want {
		return archError(fmt.Sprintf("Mach-O CPU type %#x, want %#x", cputype, want))
	}
	return nil
}

func validateELF(header []byte, p Platform) error {
	if len(header) < 20 {
		return artifactError(nil, "truncated ELF header")
	}
	if header[4] != elfClass64 {
		return artifactError(nil, "ELF artifact is not 64-bit")
	}
	var order binary.ByteOrder
	switch header[5] {
	case elfDataLE:
		order = binary.LittleEndian
	case elfDataBE:
		order = binary.BigEndian
	default:
		return artifactError(nil, "unsupported ELF byte order")
	}
	filetype := order.Uint16(header[16:18])
	if filetype != elfTypeExec && filetype != elfTypeDyn {
		return artifactError(nil, fmt.Sprintf("ELF file type %d is not executable", filetype))
	}
	machine := order.Uint16(header[18:20])
	want := uint16(elfMachineX86_64)
	if p.GOARCH == "arm64" {
		want = elfMachineAArch64
	}
	if machine != want {
		return archError(fmt.Sprintf("ELF machine %d, want %d", machine, want))
	}
	return nil
}

func artifactError(cause error, detail string) error {
	return core.FailCause(cause, core.Diagnostic{
		Category: core.CategoryComposeArtifactInvalid,
		Severity: core.SeverityError,
		Message:  msgArtifactInvalid,
		Detail:   detail,
		NextActions: []string{
			"다시 시도하고 같은 오류가 반복되면 공식 릴리스 페이지에서 파일을 직접 확인.",
			"dcraft는 검증에 실패한 파일을 설치하지 않았음.",
		},
	})
}

func archError(detail string) error {
	return core.Fail(core.Diagnostic{
		Category: core.CategoryComposeArchMismatch,
		Severity: core.SeverityError,
		Message:  msgArtifactArch,
		Detail:   detail,
		NextActions: []string{
			"이 컴퓨터의 운영체제와 아키텍처(uname -m)를 확인.",
			"다시 시도하고 같은 오류가 반복되면 공식 릴리스 페이지에서 맞는 파일을 직접 내려받으.",
			"dcraft는 다른 아키텍처의 파일을 설치하지 않았음.",
		},
	})
}

func hasPrefix(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i := range prefix {
		if b[i] != prefix[i] {
			return false
		}
	}
	return true
}
