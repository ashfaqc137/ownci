package runner

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	hdr  tar.Header
	body string
}

func buildTar(t *testing.T, entries ...entry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := e.hdr
		h.Size = int64(len(e.body))
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			h.Size = 0
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte(e.body))
		}
	}
	tw.Close()
	gz.Close()
	f.Close()
	return path
}

func file(name, body string, mode int64) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeReg, Name: name, Mode: mode}, body: body}
}
func dir(name string, mode int64) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeDir, Name: name, Mode: mode}}
}
func symlink(name, target string) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeSymlink, Name: name, Linkname: target}}
}
func hardlink(name, target string) entry {
	return entry{hdr: tar.Header{Typeflag: tar.TypeLink, Name: name, Linkname: target}}
}

func TestExtractHappyPath(t *testing.T) {
	archive := buildTar(t,
		dir("./", 0o755),
		dir("./bin/", 0o755),
		file("./bin/tool", "#!/bin/sh\n", 0o4755), // setuid must be stripped
		file("./config.sh", "x", 0o644),
		dir("./externals/", 0o755),
		dir("./externals/node/bin/", 0o755),
		file("./externals/node/bin/real", "node", 0o755),
		symlink("./externals/node/bin/node", "real"),
		symlink("./externals/link", "node/bin/real"),
		hardlink("./bin/tool2", "bin/tool"),
		file("implicit/parent/f", "y", 0o600),
		file("./readonly.txt", "r", 0o000), // owner rw must be ensured
	)
	dest := filepath.Join(t.TempDir(), "runners", "r1")
	if err := Extract(archive, dest); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dest, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != 0o755 {
		t.Errorf("mode = %v (setuid must be stripped)", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(dest, "readonly.txt")); fi.Mode().Perm() != 0o600 {
		t.Errorf("readonly mode = %v", fi.Mode().Perm())
	}
	if b, err := os.ReadFile(filepath.Join(dest, "externals", "node", "bin", "node")); err != nil || string(b) != "node" {
		t.Errorf("relative symlink: %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "externals", "link")); err != nil || string(b) != "node" {
		t.Errorf("symlink via subdir: %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "bin", "tool2")); err != nil || !strings.HasPrefix(string(b), "#!") {
		t.Errorf("hardlink: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "implicit", "parent", "f")); err != nil {
		t.Error(err)
	}
	matches, _ := filepath.Glob(dest + ".tmp-*")
	if len(matches) != 0 {
		t.Errorf("temp dir left: %v", matches)
	}
}

func TestExtractRejections(t *testing.T) {
	tests := []struct {
		name    string
		entries []entry
		wantErr string
	}{
		{"absolute path", []entry{file("/etc/evil", "x", 0o644)}, "absolute"},
		{"dotdot", []entry{file("../x", "x", 0o644)}, ".."},
		{"nested dotdot", []entry{file("a/../../x", "x", 0o644)}, ".."},
		{"empty name", []entry{file("", "x", 0o644)}, "empty"},
		{"symlink to absolute", []entry{symlink("l", "/etc")}, "absolute"},
		{"symlink escaping", []entry{symlink("l", "../outside")}, "escapes"},
		{"symlink dotdot then write", []entry{symlink("a", ".."), file("a/x", "x", 0o644)}, "escapes"},
		{"deep symlink escaping", []entry{dir("d/", 0o755), symlink("d/l", "../../x")}, "escapes"},
		{"symlink empty target", []entry{symlink("l", "")}, "empty target"},
		{"hardlink to missing", []entry{hardlink("h", "nothing")}, "not a previously extracted"},
		{"hardlink outside", []entry{hardlink("h", "../etc/passwd")}, ".."},
		{"hardlink absolute", []entry{hardlink("h", "/etc/passwd")}, "absolute"},
		{"fifo", []entry{{hdr: tar.Header{Typeflag: tar.TypeFifo, Name: "f"}}}, "unsupported"},
		{"char device", []entry{{hdr: tar.Header{Typeflag: tar.TypeChar, Name: "c"}}}, "unsupported"},
		{"block device", []entry{{hdr: tar.Header{Typeflag: tar.TypeBlock, Name: "b"}}}, "unsupported"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := buildTar(t, tc.entries...)
			parent := t.TempDir()
			dest := filepath.Join(parent, "dest")
			err := Extract(archive, dest)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			if tc.name != "empty name" && !strings.Contains(err.Error(), "archive entry") {
				t.Errorf("error should name the entry: %v", err)
			}
			if left, _ := os.ReadDir(parent); len(left) != 0 {
				t.Errorf("left behind: %v", left)
			}
		})
	}
}

func TestExtractOverwriteThroughSymlinkStaysInRoot(t *testing.T) {
	// A file entry after a symlink of the same name replaces the link; it
	// must not write through it.
	archive := buildTar(t, file("target", "orig", 0o644), symlink("l", "target"), file("l", "new", 0o644))
	dest := filepath.Join(t.TempDir(), "d")
	if err := Extract(archive, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "target")); string(b) != "orig" {
		t.Errorf("target = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "l")); string(b) != "new" {
		t.Errorf("l = %q", b)
	}
}

func TestExtractLimits(t *testing.T) {
	oldSize, oldEntries := maxTotalSize, maxEntries
	t.Cleanup(func() { maxTotalSize, maxEntries = oldSize, oldEntries })

	maxTotalSize = 10
	archive := buildTar(t, file("a", "123456", 0o644), file("b", "123456", 0o644))
	parent := t.TempDir()
	if err := Extract(archive, filepath.Join(parent, "d")); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("err = %v", err)
	}
	if left, _ := os.ReadDir(parent); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}

	maxTotalSize, maxEntries = oldSize, 2
	archive = buildTar(t, file("a", "", 0o644), file("b", "", 0o644), file("c", "", 0o644))
	if err := Extract(archive, filepath.Join(t.TempDir(), "d")); err == nil || !strings.Contains(err.Error(), "entries") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractDestExists(t *testing.T) {
	archive := buildTar(t, file("a", "x", 0o644))
	dest := t.TempDir()
	if err := Extract(archive, dest); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractBadArchive(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	os.WriteFile(bad, []byte("not gzip"), 0o600)
	parent := t.TempDir()
	if err := Extract(bad, filepath.Join(parent, "d")); err == nil {
		t.Fatal("want error")
	}
	if left, _ := os.ReadDir(parent); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
}
