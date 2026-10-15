package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	mask         = "***"
	minSecretLen = 8
	// maxLine bounds RedactWriter's buffer when the input has no newlines.
	maxLine = 1 << 20
	// keepTail is held back when a over-long line is flushed, so a secret
	// straddling the cut is still caught in the next chunk.
	keepTail = 256
)

var (
	secretsMu sync.RWMutex
	secrets   = map[string]struct{}{}

	tokenPatterns = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}`)
	tokenFlag     = regexp.MustCompile(`(--token[= ])\S+`)
)

// RegisterSecret makes Redact mask s from now on. Safe for concurrent use.
// Strings shorter than 8 bytes are ignored: masking them would mangle
// ordinary text.
func RegisterSecret(s string) {
	if len(s) < minSecretLen {
		return
	}
	secretsMu.Lock()
	secrets[s] = struct{}{}
	secretsMu.Unlock()
}

// Redact masks GitHub tokens, registered secrets and `--token <x>` arguments.
func Redact(s string) string {
	secretsMu.RLock()
	known := make([]string, 0, len(secrets))
	for sec := range secrets {
		known = append(known, sec)
	}
	secretsMu.RUnlock()
	// Longest first so a secret that contains another is masked whole.
	sort.Slice(known, func(i, j int) bool { return len(known[i]) > len(known[j]) })
	for _, sec := range known {
		s = strings.ReplaceAll(s, sec, mask)
	}
	s = tokenPatterns.ReplaceAllString(s, mask)
	return tokenFlag.ReplaceAllString(s, "${1}"+mask)
}

// RedactWriter wraps w so that every line passes through Redact.
//
// Redaction is line-buffered: bytes are held until a newline arrives, so a
// secret split across two Writes within one line is still masked. The cost is
// that a trailing partial line is only written on Close (the returned value
// implements io.Closer; callers must Close it to flush). Lines longer than
// 1 MiB are flushed in chunks, keeping a short tail back.
func RedactWriter(w io.Writer) io.WriteCloser { return &redactWriter{w: w} }

type redactWriter struct {
	mu  sync.Mutex
	w   io.Writer
	buf []byte
}

func (r *redactWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			break
		}
		line := string(r.buf[:i+1])
		r.buf = r.buf[i+1:]
		if _, err := io.WriteString(r.w, Redact(line)); err != nil {
			return len(p), err
		}
	}
	if len(r.buf) > maxLine {
		cut := len(r.buf) - keepTail
		if _, err := io.WriteString(r.w, Redact(string(r.buf[:cut]))); err != nil {
			return len(p), err
		}
		r.buf = append([]byte(nil), r.buf[cut:]...)
	}
	return len(p), nil
}

// Close flushes a trailing partial line. It does not close the wrapped writer.
func (r *redactWriter) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.buf) == 0 {
		return nil
	}
	_, err := io.WriteString(r.w, Redact(string(r.buf)))
	r.buf = nil
	return err
}

// redactHandler redacts the message and every string-ish attribute before
// delegating, so no handler or sink behind it can see a secret.
type redactHandler struct{ inner slog.Handler }

func (h redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h redactHandler) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, Redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, nr)
}

func (h redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = redactAttr(a)
	}
	return redactHandler{h.inner.WithAttrs(out)}
}

func (h redactHandler) WithGroup(name string) slog.Handler {
	return redactHandler{h.inner.WithGroup(name)}
}

func redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(v.String()))
	case slog.KindGroup:
		g := v.Group()
		out := make([]slog.Attr, len(g))
		for i, ga := range g {
			out[i] = redactAttr(ga)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, Redact(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, Redact(x.String()))
		}
	}
	return slog.Attr{Key: a.Key, Value: v}
}
