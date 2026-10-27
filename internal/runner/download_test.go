package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ashfaqc137/ownci/internal/progress"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// server serves payload at /actions-runner-test.tar.gz and counts requests.
func server(t *testing.T, payload []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.URL.Path != "/actions-runner-test.tar.gz" {
			http.NotFound(w, r)
			return
		}
		w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestFetchDownloadsAndVerifies(t *testing.T) {
	payload := []byte("pretend archive")
	srv, n := server(t, payload)
	dir := t.TempDir()
	var out bytes.Buffer
	opts := FetchOptions{
		Asset:    Asset{URL: srv.URL + "/actions-runner-test.tar.gz", SHA256: sum(payload)},
		CacheDir: dir, Progress: progress.New(&out, false),
	}
	path, err := Fetch(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, payload) {
		t.Errorf("content = %q", got)
	}
	if filepath.Base(path) != "actions-runner-test.tar.gz" || !strings.Contains(out.String(), "done") {
		t.Errorf("path %s, progress %q", path, out.String())
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %04o", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".partial"); !os.IsNotExist(err) {
		t.Error("partial file left behind")
	}

	// Second call: cache hit, no request.
	if _, err := Fetch(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1", n.Load())
	}
}

func TestFetchCorruptCacheRedownloads(t *testing.T) {
	payload := []byte("pretend archive")
	srv, n := server(t, payload)
	dir := t.TempDir()
	cached := filepath.Join(dir, "actions-runner-test.tar.gz")
	if err := os.WriteFile(cached, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	path, err := Fetch(context.Background(), FetchOptions{
		Asset:    Asset{URL: srv.URL + "/actions-runner-test.tar.gz", SHA256: sum(payload)},
		CacheDir: dir, Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, payload) || n.Load() != 1 {
		t.Errorf("content %q, requests %d", got, n.Load())
	}
	if !strings.Contains(logs.String(), "checksum mismatch") || !strings.Contains(logs.String(), "re-downloading") {
		t.Errorf("logs = %s", logs.String())
	}
}

func TestFetchWrongBytes(t *testing.T) {
	srv, _ := server(t, []byte("evil bytes"))
	dir := t.TempDir()
	_, err := Fetch(context.Background(), FetchOptions{
		Asset:    Asset{URL: srv.URL + "/actions-runner-test.tar.gz", SHA256: sum([]byte("expected"))},
		CacheDir: dir,
	})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("err = %v", err)
	}
	var re *Error
	if !errors.As(err, &re) || re.Hint == "" || !strings.Contains(re.Msg, sum([]byte("expected"))) {
		t.Errorf("err = %#v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files left behind: %v", entries)
	}
}

func TestFetchHTTPError(t *testing.T) {
	srv, _ := server(t, nil)
	_, err := Fetch(context.Background(), FetchOptions{
		Asset:    Asset{URL: srv.URL + "/missing.tar.gz", SHA256: "abc"},
		CacheDir: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "/missing.tar.gz") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchRequiresChecksum(t *testing.T) {
	if _, err := Fetch(context.Background(), FetchOptions{Asset: Asset{URL: "http://x/y.tgz"}, CacheDir: t.TempDir()}); err == nil {
		t.Fatal("want error")
	}
}

func TestAssetFileNameIgnoresQuery(t *testing.T) {
	if got := (Asset{URL: "https://h/p/a.tar.gz?x=1#f"}).fileName(); got != "a.tar.gz" {
		t.Errorf("got %q", got)
	}
}

func TestPinnedAssets(t *testing.T) {
	for _, arch := range []string{"linux-x64", "linux-arm64"} {
		a, err := AssetFor(arch)
		if err != nil {
			t.Fatal(err)
		}
		want := "https://github.com/actions/runner/releases/download/v" + Pinned.Version + "/actions-runner-" + arch + "-" + Pinned.Version + ".tar.gz"
		if a.URL != want || len(a.SHA256) != 64 || a.Version != Pinned.Version {
			t.Errorf("%s: %+v", arch, a)
		}
	}
	if _, err := AssetFor(""); err == nil {
		t.Error("empty arch should fail")
	}
}
