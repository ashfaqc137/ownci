// Package testutil holds fixtures shared by tests across packages.
package testutil

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fake runner mimics the real actions/runner tarball closely enough for
// tests and the dev harness. Its scripts live in fakerunner/ as real files.
//
// Controls (see the scripts for the full contract):
//   - .fake-job-request in the runner dir: "<job> [nocontainer]" → run.sh
//     simulates one job, running ACTIONS_RUNNER_HOOK_JOB_COMPLETED if set.
//   - FAKE_RUNNER_EXIT_CODE=N (env or .env): run.sh exits N after connecting.
//   - OWNCI_DEV_GITHUB_API_URL: config.sh calls <url>/_fake/register.
//
//go:embed fakerunner
var fakeRunnerFS embed.FS

// FakeRunnerOptions configures the fake runner.
type FakeRunnerOptions struct {
	Version string // default "2.999.0-fake"
}

func (o FakeRunnerOptions) version() string {
	if o.Version == "" {
		return "2.999.0-fake"
	}
	return o.Version
}

type fakeFile struct {
	name string // relative, slash-separated
	data []byte
	mode fs.FileMode
}

func fakeRunnerFiles(opts FakeRunnerOptions) []fakeFile {
	var out []fakeFile
	err := fs.WalkDir(fakeRunnerFS, "fakerunner", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fakeRunnerFS.ReadFile(p)
		if err != nil {
			return err
		}
		b = bytes.ReplaceAll(b, []byte("__FAKE_VERSION__"), []byte(opts.version()))
		out = append(out, fakeFile{name: strings.TrimPrefix(p, "fakerunner/"), data: b, mode: 0o755})
		return nil
	})
	if err != nil {
		panic(err) // the embed is static; this cannot fail
	}
	return out
}

// FakeRunnerTarball builds a deterministic .tar.gz shaped like the real
// runner archive (entries start with "./") and returns it with its SHA256.
func FakeRunnerTarball(opts FakeRunnerOptions) (data []byte, sha256hex string) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	epoch := time.Unix(0, 0)
	dirs := map[string]bool{}
	writeDir := func(name string) {
		if dirs[name] {
			return
		}
		dirs[name] = true
		must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: 0o755, ModTime: epoch}))
	}
	must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: "./", Mode: 0o755, ModTime: epoch}))
	for _, f := range fakeRunnerFiles(opts) {
		if d := path.Dir(f.name); d != "." {
			writeDir("./" + d)
		}
		must(tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "./" + f.name, Mode: int64(f.mode), Size: int64(len(f.data)), ModTime: epoch}))
		_, err := tw.Write(f.data)
		must(err)
	}
	must(tw.Close())
	must(gz.Close())
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// WriteFakeRunner writes an already-extracted fake runner tree into dir.
func WriteFakeRunner(t testing.TB, dir string, opts ...FakeRunnerOptions) {
	t.Helper()
	var o FakeRunnerOptions
	if len(opts) > 0 {
		o = opts[0]
	}
	for _, f := range fakeRunnerFiles(o) {
		p := filepath.Join(dir, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, f.data, f.mode); err != nil {
			t.Fatal(err)
		}
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
