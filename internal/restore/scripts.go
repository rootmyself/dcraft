package restore

import (
	"fmt"
	"os"
	"strings"

	"github.com/rootmyself/dcraft/internal/backup"
)

// WriteVerifyScript writes a POSIX/BusyBox shell script that checks one
// restored volume against the validated manifest. It runs inside the root
// helper image after extraction. Each entry's content hash, type, uid, gid and
// mode are checked, hardlinks must share an inode with their target, symlink
// targets must match, and the total entry count must be exact so stale files
// from a merge cannot pass.
func WriteVerifyScript(path string, m backup.Manifest) error {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -u\n")
	b.WriteString("fail() { printf 'dcraft-restore-verify: %s\\n' \"$1\"; exit 3; }\n")
	b.WriteString("meta() {\n")
	b.WriteString("  p=$1; want_u=$2; want_g=$3; want_m=$4\n")
	b.WriteString("  vals=$(stat -c '%u %g %a' \"$p\") || fail \"stat: $p\"\n")
	b.WriteString("  set -- $vals\n")
	b.WriteString("  [ \"$1\" = \"$want_u\" ] || fail \"uid: $p\"\n")
	b.WriteString("  [ \"$2\" = \"$want_g\" ] || fail \"gid: $p\"\n")
	b.WriteString("  [ \"$3\" = \"$want_m\" ] || fail \"mode: $p\"\n")
	b.WriteString("}\n")

	count := 0
	for _, entry := range m.Entries {
		rel := normalizeEntryPath(entry.Path)
		if rel == "" {
			continue
		}
		count++
		p := shellQuote("/data/" + rel)
		mode := normalizedMode(entry.Mode)
		meta := fmt.Sprintf("meta %s %d %d %s", p, entry.UID, entry.GID, shellQuote(mode))
		switch entry.Type {
		case backup.EntryFile:
			fmt.Fprintf(&b, "[ -f %s ] && [ ! -L %s ] || fail %s\n", p, p, shellQuote("type: /data/"+rel))
			b.WriteString(meta)
			b.WriteString("\n")
			fmt.Fprintf(&b, "[ \"$(sha256sum %s | cut -d' ' -f1)\" = %s ] || fail %s\n",
				p, shellQuote(strings.ToLower(entry.SHA256)), shellQuote("sha256: /data/"+rel))
		case backup.EntryDir:
			fmt.Fprintf(&b, "[ -d %s ] && [ ! -L %s ] || fail %s\n", p, p, shellQuote("type: /data/"+rel))
			b.WriteString(meta)
			b.WriteString("\n")
		case backup.EntrySymlink:
			fmt.Fprintf(&b, "[ -L %s ] || fail %s\n", p, shellQuote("type: /data/"+rel))
			fmt.Fprintf(&b, "[ \"$(readlink %s)\" = %s ] || fail %s\n",
				p, shellQuote(entry.LinkTarget), shellQuote("link: /data/"+rel))
			b.WriteString(meta)
			b.WriteString("\n")
		case backup.EntryHardlink:
			targetRel := normalizeEntryPath(entry.LinkTarget)
			if targetRel == "" {
				return fmt.Errorf("%w: hardlink %q has an empty target", ErrUnsafeLink, entry.Path)
			}
			target := shellQuote("/data/" + targetRel)
			fmt.Fprintf(&b, "[ -f %s ] && [ ! -L %s ] || fail %s\n", p, p, shellQuote("type: /data/"+rel))
			b.WriteString(meta)
			b.WriteString("\n")
			fmt.Fprintf(&b, "[ \"$(stat -c '%%i' %s)\" = \"$(stat -c '%%i' %s)\" ] || fail %s\n",
				p, target, shellQuote("hardlink: /data/"+rel))
		case backup.EntryOther:
			fmt.Fprintf(&b, "[ -p %s ] || [ -c %s ] || [ -b %s ] || [ -S %s ] || fail %s\n",
				p, p, p, p, shellQuote("special: /data/"+rel))
			b.WriteString(meta)
			b.WriteString("\n")
		default:
			return fmt.Errorf("%w: unsupported entry type %q", ErrArtifactInvalid, entry.Type)
		}
	}
	fmt.Fprintf(&b, "actual=$(find /data -mindepth 1 | wc -l) || fail %s\n", shellQuote("entry count read"))
	fmt.Fprintf(&b, "[ \"$actual\" -eq %d ] || fail \"entry count: $actual != %d\"\n", count, count)
	b.WriteString("printf 'ok\\n'\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		return err
	}
	return nil
}

func normalizedMode(raw string) string {
	var mode uint32
	if _, err := fmt.Sscanf(raw, "%o", &mode); err != nil {
		return "0"
	}
	return fmt.Sprintf("%o", mode&0o7777)
}

// shellQuote makes an arbitrary validated member path safe inside a POSIX
// single-quoted string. NUL and backslash are rejected by the backup format;
// newlines and quotes are preserved literally.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
