package progress

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlainStepPrintsALine(t *testing.T) {
	var b bytes.Buffer
	p := New(&b, false)
	p.Step("Registering the runner")
	p.End()
	if got := b.String(); got != "Registering the runner…\n" {
		t.Errorf("output %q", got)
	}
}

func TestPlainDownloadPrintsQuarterMarks(t *testing.T) {
	var b bytes.Buffer
	tr := New(&b, false).Download("runner.tar.gz", 8<<20)
	for i := 0; i < 8; i++ {
		tr.Write(make([]byte, 1<<20))
	}
	tr.Finish(true)
	if got, want := b.String(), "Downloading runner.tar.gz (8 MB)… 25% 50% 75% done\n"; got != want {
		t.Errorf("output %q, want %q", got, want)
	}
}

func TestPlainDownloadFailure(t *testing.T) {
	var b bytes.Buffer
	tr := New(&b, false).Download("runner.tar.gz", -1)
	tr.Write([]byte("x"))
	tr.Finish(false)
	if got := b.String(); got != "Downloading runner.tar.gz… failed\n" {
		t.Errorf("output %q", got)
	}
}

func TestLiveDownloadRedrawsAndEndsWithASummary(t *testing.T) {
	var b bytes.Buffer
	tr := New(&b, true).Download("runner.tar.gz", 4<<20)
	tr.Write(make([]byte, 2<<20))
	tr.Finish(true)
	out := b.String()
	if !strings.HasPrefix(out, "\r\033[KDownloading runner.tar.gz:   0% · 0 MB of 4 MB") {
		t.Errorf("no live line: %q", out)
	}
	if !strings.HasSuffix(out, "\r\033[KDownloading runner.tar.gz (4 MB)… done (0s)\n") {
		t.Errorf("no summary: %q", out)
	}
}

func TestLiveStepIsClearedByEnd(t *testing.T) {
	var b bytes.Buffer
	p := New(&b, true)
	p.Step("Extracting the runner")
	p.End()
	p.End() // idempotent
	out := b.String()
	if !strings.Contains(out, "Extracting the runner…") || !strings.HasSuffix(out, "\r\033[K") || strings.Count(out, "\r\033[K") < 2 {
		t.Errorf("output %q", out)
	}
}

func TestNilPrinterIsSilent(t *testing.T) {
	var p *Printer
	p.Step("x")
	tr := p.Download("x", 10)
	tr.Write([]byte("abc"))
	tr.Finish(true)
	p.End()
}
