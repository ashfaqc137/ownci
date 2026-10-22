package runner

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EnvMarker introduces the keys OwnCI manages. It has no '=', so the Listener
// (which only reads KEY=VALUE lines) ignores it.
const EnvMarker = "# --- managed by ownci: do not edit the keys below ---"

// EnvPath is the file Runner.Listener loads at startup.
func EnvPath(dir string) string { return filepath.Join(dir, ".env") }

// EnsureEnv makes <dir>/.env contain every managed KEY=VALUE, touching nothing
// else: config.sh writes its own lines (LANG, PATH, …) into this file, so it
// is edited, never replaced. A managed key that is present is corrected in
// place (the first occurrence is kept, duplicates are dropped); missing ones
// are appended below EnvMarker. Every line setting one of the absent keys is
// removed. It reports whether the file changed.
func EnsureEnv(dir string, managed map[string]string, absent ...string) (changed bool, err error) {
	for k, v := range managed {
		if k == "" || strings.ContainsAny(k, "=\r\n") || strings.ContainsAny(v, "\r\n") {
			return false, fmt.Errorf("invalid env entry %q", k)
		}
	}
	drop := map[string]bool{}
	for _, k := range absent {
		if _, both := managed[k]; both || k == "" || strings.ContainsAny(k, "=\r\n") {
			return false, fmt.Errorf("invalid absent env key %q", k)
		}
		drop[k] = true
	}
	path := EnvPath(dir)
	orig, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", path, err)
	}

	eol := "\n"
	if bytes.Contains(orig, []byte("\r\n")) {
		eol = "\r\n"
	}
	var lines []string
	if len(orig) > 0 {
		text := strings.TrimSuffix(string(orig), "\n")
		lines = strings.Split(text, "\n")
		for i := range lines {
			lines[i] = strings.TrimSuffix(lines[i], "\r")
		}
	}

	seen := map[string]bool{}
	hasMarker := false
	out := make([]string, 0, len(lines)+len(managed)+1)
	for _, line := range lines {
		if line == EnvMarker {
			hasMarker = true
		}
		key, _, ok := strings.Cut(line, "=")
		if ok && drop[key] {
			continue
		}
		want, isManaged := managed[key]
		if !ok || !isManaged {
			out = append(out, line)
			continue
		}
		if seen[key] {
			continue // duplicate of a managed key
		}
		seen[key] = true
		out = append(out, key+"="+want)
	}

	var missing []string
	for k := range managed {
		if !seen[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		if !hasMarker {
			out = append(out, EnvMarker)
		}
		for _, k := range missing {
			out = append(out, k+"="+managed[k])
		}
	}

	var buf strings.Builder
	for _, l := range out {
		buf.WriteString(l)
		buf.WriteString(eol)
	}
	if buf.String() == string(orig) {
		return false, nil
	}
	if err := writeFileAtomic(path, []byte(buf.String()), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// ReadEnv parses <dir>/.env like the Listener: KEY=VALUE split on the first
// '=', lines without '=' ignored, the last occurrence wins. A missing file is
// an empty environment.
func ReadEnv(dir string) (map[string]string, error) {
	b, err := os.ReadFile(EnvPath(dir))
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", EnvPath(dir), err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if k, v, ok := strings.Cut(line, "="); ok && k != "" {
			env[k] = v
		}
	}
	return env, nil
}

// writeFileAtomic writes via a temp file in the same directory and a rename.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("setting permissions on %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}
