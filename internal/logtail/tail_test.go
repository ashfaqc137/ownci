package logtail

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ashfaqc137/ownci/internal/logging"
)

func writeLines(t *testing.T, path string, n int) {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTail(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log")

	t.Run("small file", func(t *testing.T) {
		writeLines(t, p, 10)
		got, size, err := Tail(p, 3)
		if err != nil || strings.Join(got, "|") != "line 8|line 9|line 10" {
			t.Errorf("%v %v", got, err)
		}
		if fi, _ := os.Stat(p); size != fi.Size() {
			t.Errorf("size = %d", size)
		}
	})
	t.Run("fewer lines than asked", func(t *testing.T) {
		writeLines(t, p, 3)
		if got, _, _ := Tail(p, 100); len(got) != 3 || got[0] != "line 1" {
			t.Errorf("%v", got)
		}
	})
	t.Run("large file (over 1 MiB)", func(t *testing.T) {
		const total = 100000
		writeLines(t, p, total)
		if fi, _ := os.Stat(p); fi.Size() < 1<<20 {
			t.Fatalf("file only %d bytes", fi.Size())
		}
		got, _, err := Tail(p, 5)
		want := "line 99996|line 99997|line 99998|line 99999|line 100000"
		if err != nil || strings.Join(got, "|") != want {
			t.Errorf("%v %v", got, err)
		}
		// Lines straddling a chunk boundary are intact.
		got, _, _ = Tail(p, 20000)
		if len(got) != 20000 || got[0] != "line 80001" || got[19999] != "line 100000" {
			t.Errorf("len=%d first=%q last=%q", len(got), got[0], got[len(got)-1])
		}
	})
	t.Run("no trailing newline", func(t *testing.T) {
		if err := os.WriteFile(p, []byte("a\nb\nc"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := Tail(p, 2); strings.Join(got, "|") != "b|c" {
			t.Errorf("%v", got)
		}
	})
	t.Run("empty and n=0", func(t *testing.T) {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _, err := Tail(p, 5); err != nil || len(got) != 0 {
			t.Errorf("%v %v", got, err)
		}
		writeLines(t, p, 3)
		if got, _, _ := Tail(p, 0); len(got) != 0 {
			t.Errorf("%v", got)
		}
	})
	t.Run("CRLF", func(t *testing.T) {
		if err := os.WriteFile(p, []byte("a\r\nb\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got, _, _ := Tail(p, 2); strings.Join(got, "|") != "a|b" {
			t.Errorf("%q", got)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, _, err := Tail(filepath.Join(dir, "nope"), 3); !os.IsNotExist(err) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("redaction", func(t *testing.T) {
		logging.RegisterSecret("s3cr3t-value-123")
		content := "token ghp_abcdefghijklmnopqrstuvwxyz0123456789\nregistered s3cr3t-value-123\nconfig --token abc123def456\n"
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		got, _, _ := Tail(p, 10)
		joined := strings.Join(got, "\n")
		for _, leak := range []string{"ghp_abc", "s3cr3t-value-123", "abc123def456"} {
			if strings.Contains(joined, leak) {
				t.Errorf("%q leaked: %s", leak, joined)
			}
		}
	})
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func waitFor(t *testing.T, buf *syncBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q; got %q", want, buf.String())
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func TestFollow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	writeLines(t, p, 3)
	_, size, _ := Tail(p, 1)

	ctx, cancel := context.WithCancel(context.Background())
	var out syncBuf
	done := make(chan error, 1)
	go func() { done <- Follow(ctx, &out, p, size, 10*time.Millisecond) }()

	appendTo(t, p, "new one\nnew ")
	waitFor(t, &out, "new one\n")
	if strings.Contains(out.String(), "new \n") {
		t.Error("a partial line was printed early")
	}
	appendTo(t, p, "two\n")
	waitFor(t, &out, "new two\n")

	// Truncation: start over from the top.
	if err := os.WriteFile(p, []byte("after truncate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &out, "after truncate\n")

	// Rotation: the file is replaced by a new one.
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("rotated 1\nrotated 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, &out, "rotated 2\n")

	// Removed and recreated.
	os.Remove(p)
	time.Sleep(30 * time.Millisecond)
	appendTo(t, p, "reborn\n")
	waitFor(t, &out, "reborn\n")

	// Redaction applies while following.
	appendTo(t, p, "tok ghp_abcdefghijklmnopqrstuvwxyz0123456789\n")
	waitFor(t, &out, "tok ***")
	if strings.Contains(out.String(), "ghp_abc") {
		t.Error("token leaked while following")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Follow = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Follow did not return after cancel")
	}
	if strings.Contains(out.String(), "line 1") {
		t.Error("Follow re-printed old content")
	}
}
