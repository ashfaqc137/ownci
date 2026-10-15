package runner

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Extraction limits; vars so tests can lower them.
var (
	maxTotalSize int64 = 2 << 30
	maxEntries         = 100_000
)

// Extract unpacks a .tar.gz into dest, which must not exist. It extracts
// into a sibling temp dir and renames it into place, so dest is either
// complete or absent. Every write goes through os.Root, so even a hostile
// archive cannot write outside the extraction root.
func Extract(archivePath, dest string) (err error) {
	if _, statErr := os.Lstat(dest); statErr == nil {
		return fmt.Errorf("extract destination %s already exists", dest)
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", dest, statErr)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("creating parent of %s: %w", dest, err)
	}
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := dest + ".tmp-" + hex.EncodeToString(rnd[:])
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer func() {
		if err != nil {
			os.RemoveAll(tmp)
		}
	}()

	if err := extractInto(archivePath, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("moving extracted runner into place: %w", err)
	}
	return nil
}

func extractInto(archivePath, dir string) error {
	af, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	defer af.Close()
	gz, err := gzip.NewReader(af)
	if err != nil {
		return fmt.Errorf("reading archive: %w", err)
	}
	defer gz.Close()

	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("opening extraction root: %w", err)
	}
	defer root.Close()

	tr := tar.NewReader(gz)
	files := map[string]bool{} // regular files extracted so far, for hardlinks
	var total int64
	entries := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}
		entries++
		if entries > maxEntries {
			return fmt.Errorf("archive has more than %d entries", maxEntries)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name, err := cleanName(hdr.Name)
		if err != nil {
			return entryErr(hdr.Name, err)
		}
		if name == "." {
			continue // the "./" root entry
		}
		if err := extractEntry(root, tr, hdr, name, files, &total); err != nil {
			return entryErr(hdr.Name, err)
		}
	}
}

func entryErr(name string, err error) error {
	return fmt.Errorf("archive entry %q: %w", name, err)
}

// cleanName validates an archive path and returns it cleaned and relative.
func cleanName(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty name")
	}
	if strings.HasPrefix(name, "/") {
		return "", errors.New("absolute path")
	}
	for _, el := range strings.Split(name, "/") {
		if el == ".." {
			return "", errors.New("path contains '..'")
		}
	}
	return path.Clean(name), nil
}

func extractEntry(root *os.Root, tr *tar.Reader, hdr *tar.Header, name string, files map[string]bool, total *int64) error {
	// Keep permission bits; drop setuid/setgid/sticky.
	perm := fs.FileMode(hdr.Mode) & 0o777

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := root.MkdirAll(name, 0o755); err != nil {
			return err
		}
		return root.Chmod(name, perm|0o700)

	case tar.TypeReg, tar.TypeRegA:
		*total += hdr.Size
		if *total > maxTotalSize {
			return fmt.Errorf("archive exceeds the %d byte size limit", maxTotalSize)
		}
		if err := prepareParent(root, name); err != nil {
			return err
		}
		root.Remove(name) // tolerate duplicate entries; never write through an earlier symlink
		f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		if err := f.Chmod(perm | 0o600); err != nil {
			f.Close()
			return err
		}
		files[name] = true
		return f.Close()

	case tar.TypeSymlink:
		if hdr.Linkname == "" {
			return errors.New("symlink with empty target")
		}
		if strings.HasPrefix(hdr.Linkname, "/") {
			return fmt.Errorf("symlink to absolute target %q", hdr.Linkname)
		}
		// Resolve lexically against the link's directory; it must stay inside.
		resolved := path.Join(path.Dir(name), hdr.Linkname)
		if resolved == ".." || strings.HasPrefix(resolved, "../") {
			return fmt.Errorf("symlink target %q escapes the extraction root", hdr.Linkname)
		}
		if err := prepareParent(root, name); err != nil {
			return err
		}
		root.Remove(name)
		return root.Symlink(hdr.Linkname, name)

	case tar.TypeLink:
		target, err := cleanName(hdr.Linkname)
		if err != nil {
			return fmt.Errorf("hardlink target %q: %w", hdr.Linkname, err)
		}
		if !files[target] {
			return fmt.Errorf("hardlink target %q is not a previously extracted file", hdr.Linkname)
		}
		if err := prepareParent(root, name); err != nil {
			return err
		}
		root.Remove(name)
		return root.Link(target, name)

	default:
		return fmt.Errorf("unsupported entry type %q", string(hdr.Typeflag))
	}
}

func prepareParent(root *os.Root, name string) error {
	if dir := path.Dir(name); dir != "." {
		return root.MkdirAll(dir, 0o755)
	}
	return nil
}
