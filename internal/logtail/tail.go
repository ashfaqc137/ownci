// Package logtail reads the last lines of a log file and follows it, for
// `ownci logs`. Output goes through the secret redactor.
package logtail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ashfaqc137/ownci/internal/logging"
)

const chunk = 64 * 1024

// Tail returns the last n lines of path without reading the whole file: it
// reads backwards in chunks until it has seen enough newlines. The final line
// counts even without a trailing newline. It returns the offset just past the
// data it read (the file size), which Follow continues from.
func Tail(path string, n int) (lines []string, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	size = fi.Size()
	if n <= 0 || size == 0 {
		return nil, size, nil
	}

	var buf []byte
	pos := size
	newlines := 0
	for pos > 0 && newlines <= n { // n+1 newlines guarantee n complete lines
		step := int64(chunk)
		if pos < step {
			step = pos
		}
		pos -= step
		part := make([]byte, step)
		if _, err := f.ReadAt(part, pos); err != nil && !errors.Is(err, io.EOF) {
			return nil, size, err
		}
		buf = append(part, buf...)
		newlines = bytes.Count(buf, []byte{'\n'})
	}
	text := strings.TrimSuffix(string(buf), "\n")
	all := strings.Split(text, "\n")
	if pos > 0 && len(all) > 0 {
		all = all[1:] // the first line is cut in the middle
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for i := range all {
		all[i] = logging.Redact(strings.TrimSuffix(all[i], "\r"))
	}
	return all, size, nil
}

// Print writes the last n lines of path to w.
func Print(w io.Writer, path string, n int) (size int64, err error) {
	lines, size, err := Tail(path, n)
	if err != nil {
		return 0, err
	}
	for _, l := range lines {
		fmt.Fprintln(w, l)
	}
	return size, nil
}

// Follow prints lines appended to path after offset, until ctx ends. It polls
// every interval, starts over at the top when the file is truncated or
// replaced (rotation), and waits for the file to reappear if it is removed.
func Follow(ctx context.Context, w io.Writer, path string, offset int64, interval time.Duration) error {
	var (
		cur     os.FileInfo
		pending string // an unfinished line, printed once its newline arrives
	)
	if fi, err := os.Stat(path); err == nil {
		cur = fi
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		fi, err := os.Stat(path)
		if err != nil {
			continue // removed: wait for it to come back
		}
		if cur != nil && !os.SameFile(cur, fi) {
			offset, pending = 0, "" // replaced
		}
		if fi.Size() < offset {
			offset, pending = 0, "" // truncated
		}
		cur = fi
		if fi.Size() == offset {
			continue
		}
		data, err := readFrom(path, offset)
		if err != nil {
			continue
		}
		offset += int64(len(data))
		pending += string(data)
		for {
			i := strings.IndexByte(pending, '\n')
			if i < 0 {
				break
			}
			fmt.Fprintln(w, logging.Redact(strings.TrimSuffix(pending[:i], "\r")))
			pending = pending[i+1:]
		}
	}
}

func readFrom(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
