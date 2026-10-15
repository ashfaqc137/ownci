package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrChecksumMismatch means a downloaded archive did not match its pinned SHA256.
var ErrChecksumMismatch = errors.New("checksum mismatch")

// FetchOptions configures Fetch. HTTP, Progress and Logger may be nil.
type FetchOptions struct {
	Asset    Asset
	CacheDir string
	HTTP     *http.Client
	Progress io.Writer
	Logger   *slog.Logger
}

// defaultHTTPClient has no overall timeout (the archive is large); the
// transport bounds connection setup and the wait for response headers, and
// the context bounds everything else.
func defaultHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}}
}

// Fetch returns the path of a verified archive in the cache, downloading it
// when needed. The file is verified before it is moved into place, so a
// cached file is always one that matched the checksum when written; it is
// re-hashed on reuse anyway.
func Fetch(ctx context.Context, o FetchOptions) (string, error) {
	log := o.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Asset.SHA256 == "" {
		return "", errors.New("refusing to download without an expected SHA256")
	}
	if err := os.MkdirAll(o.CacheDir, 0o700); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}
	path := filepath.Join(o.CacheDir, o.Asset.fileName())

	if got, err := hashFile(path); err == nil {
		if strings.EqualFold(got, o.Asset.SHA256) {
			log.Debug("runner archive cache hit", "path", path)
			return path, nil
		}
		log.Warn("checksum mismatch in cached runner archive, re-downloading", "path", path, "expected", o.Asset.SHA256, "actual", got)
		if err := os.Remove(path); err != nil {
			return "", fmt.Errorf("removing corrupt cache file: %w", err)
		}
	}

	client := o.HTTP
	if client == nil {
		client = defaultHTTPClient()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.Asset.URL, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", &Error{Msg: fmt.Sprintf("downloading %s: %v", o.Asset.URL, err), Hint: "Check your network connection and proxy settings, then retry."}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &Error{Msg: fmt.Sprintf("downloading %s: HTTP %s", o.Asset.URL, resp.Status), Hint: "Retry later; if it persists the pinned runner version may have been removed."}
	}

	partial := path + ".partial"
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating %s: %w", partial, err)
	}
	committed, progressOpen := false, false
	defer func() {
		f.Close()
		if !committed {
			os.Remove(partial)
			if progressOpen {
				fmt.Fprintln(o.Progress, "failed")
			}
		}
	}()

	if o.Progress != nil {
		progressOpen = true
		if resp.ContentLength > 0 {
			fmt.Fprintf(o.Progress, "Downloading %s (%.0f MB)… ", o.Asset.fileName(), float64(resp.ContentLength)/(1<<20))
		} else {
			fmt.Fprintf(o.Progress, "Downloading %s… ", o.Asset.fileName())
		}
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", fmt.Errorf("downloading %s: %w", o.Asset.URL, err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("syncing %s: %w", partial, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("closing %s: %w", partial, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, o.Asset.SHA256) {
		return "", &Error{
			Msg:  fmt.Sprintf("checksum mismatch for %s: expected %s, got %s", o.Asset.fileName(), o.Asset.SHA256, got),
			Hint: "The download was corrupted or tampered with; retry, and report it if it persists.",
			Err:  ErrChecksumMismatch,
		}
	}
	if err := os.Rename(partial, path); err != nil {
		return "", fmt.Errorf("moving archive into place: %w", err)
	}
	committed = true
	if o.Progress != nil {
		fmt.Fprintln(o.Progress, "done")
	}
	return path, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
