package logging

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
)

const (
	mask         = "***"
	minSecretLen = 8
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
