// Package progress reports long-running steps. On a terminal it keeps one
// status line updated in place (a spinner with the elapsed time, or a
// download's percentage, size and speed) that the step's result replaces.
// Elsewhere (pipes, logs, tests) it prints append-only text, so the output
// still shows which step was running when something stops.
//
// A nil *Printer is valid and prints nothing.
package progress

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

const tick = 100 * time.Millisecond

var spinner = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// Printer reports progress to w. Step, Download and End must be called from
// one goroutine, and nothing else may write to w while a live line is shown:
// call End first.
type Printer struct {
	w    io.Writer
	live bool
	stop chan struct{} // non-nil while a live line is shown
	done chan struct{}
}

// New returns a Printer. live means w is a terminal that can redraw a line.
func New(w io.Writer, live bool) *Printer { return &Printer{w: w, live: live} }

// Step announces work that may take a while, until End or the next Step.
func (p *Printer) Step(msg string) {
	if p == nil {
		return
	}
	p.End()
	if !p.live {
		fmt.Fprintf(p.w, "%s…\n", msg)
		return
	}
	start, i := time.Now(), 0
	p.run(func() string {
		i++
		s := fmt.Sprintf("%c %s…", spinner[i%len(spinner)], msg)
		if el := time.Since(start); el >= time.Second {
			s += fmt.Sprintf(" (%ds)", int(el.Seconds()))
		}
		return s
	})
}

// End clears the live line, if any. It is safe to call at any time.
func (p *Printer) End() {
	if p == nil || p.stop == nil {
		return
	}
	close(p.stop)
	<-p.done
	p.stop, p.done = nil, nil
	fmt.Fprint(p.w, "\r\033[K")
}

// run draws render now and then on every tick until End.
func (p *Printer) run(render func() string) {
	p.stop, p.done = make(chan struct{}), make(chan struct{})
	stop, done := p.stop, p.done
	draw := func() { fmt.Fprintf(p.w, "\r\033[K%s", render()) }
	draw()
	go func() {
		defer close(done)
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				draw()
			}
		}
	}()
}

// Transfer reports a download. Write it the downloaded bytes and call Finish.
type Transfer struct {
	p     *Printer
	label string
	total int64
	n     atomic.Int64
	start time.Time
	marks int // plain mode: quarter marks printed so far
}

// Download starts reporting the download of label; total is its size in
// bytes, or ≤ 0 when unknown.
func (p *Printer) Download(label string, total int64) *Transfer {
	t := &Transfer{p: p, label: label, total: total, start: time.Now()}
	if p == nil {
		return t
	}
	p.End()
	if p.live {
		p.run(t.render)
	} else {
		fmt.Fprintf(p.w, "%s… ", t.title())
	}
	return t
}

func (t *Transfer) Write(b []byte) (int, error) {
	n := t.n.Add(int64(len(b)))
	if t.p != nil && !t.p.live && t.total > 0 {
		for t.marks < 3 && n*4 >= t.total*int64(t.marks+1) {
			t.marks++
			fmt.Fprintf(t.p.w, "%d%% ", t.marks*25)
		}
	}
	return len(b), nil
}

// Finish ends the report with "done" or "failed".
func (t *Transfer) Finish(ok bool) {
	p := t.p
	if p == nil {
		return
	}
	word := "failed"
	if ok {
		word = "done"
	}
	if p.live {
		p.End()
		fmt.Fprintf(p.w, "%s… %s (%s)\n", t.title(), word, time.Since(t.start).Round(time.Second))
		return
	}
	fmt.Fprintln(p.w, word)
}

func (t *Transfer) title() string {
	if t.total > 0 {
		return fmt.Sprintf("Downloading %s (%s)", t.label, mb(t.total))
	}
	return "Downloading " + t.label
}

func (t *Transfer) render() string {
	n := t.n.Load()
	s := "Downloading " + t.label + ": "
	if t.total > 0 {
		s += fmt.Sprintf("%3d%% · %s of %s", n*100/t.total, mb(n), mb(t.total))
	} else {
		s += mb(n)
	}
	if el := time.Since(t.start).Seconds(); el >= 1 {
		s += fmt.Sprintf(" · %.1f MB/s", float64(n)/el/(1<<20))
	}
	return s
}

func mb(n int64) string { return fmt.Sprintf("%.0f MB", float64(n)/(1<<20)) }
