package uninstall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ashfaqc137/ownci/internal/config"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/hooks"
	"github.com/ashfaqc137/ownci/internal/runner"
	"github.com/ashfaqc137/ownci/internal/supervisor"
)

// purgeResult records what purge did. Nothing is ever deleted outside the
// known layout of the data directory.
type purgeResult struct {
	DataDirRemoved bool
	OthersRemain   bool     // another runner keeps the shared files alive
	Leftovers      []string // paths that could not be removed or are not OwnCI's
	Hints          []string
}

func (p purgeResult) describe(root string) string {
	switch {
	case p.DataDirRemoved:
		return "removed (" + root + " is gone)"
	case p.OthersRemain:
		return "this runner's files removed; " + root + " kept (other runners remain)"
	case len(p.Leftovers) > 0:
		return "partly removed; " + root + " kept because of what is left"
	}
	return "removed; " + root + " kept"
}

// checkPurgeSafe refuses to purge anything that does not look like an OwnCI
// data directory, or that is dangerously broad. It runs before any step with
// side effects.
func checkPurgeSafe(p config.Paths, home string) error {
	root := p.Root
	fi, err := os.Lstat(p.Marker)
	if err != nil || !fi.Mode().IsRegular() {
		return &Error{
			Msg:  fmt.Sprintf("%s is not an OwnCI data directory (the %s marker is missing)", root, config.MarkerName),
			Hint: "Nothing was deleted. --purge only removes directories ownci created itself; delete this one by hand if you are sure.",
		}
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return &Error{Msg: fmt.Sprintf("cannot resolve %s: %v", root, err), Err: err}
	}
	if real == "/" || len(strings.Split(strings.Trim(real, "/"), "/")) < 2 {
		return &Error{Msg: fmt.Sprintf("refusing to purge %s: too close to the filesystem root", real),
			Hint: "Use a dedicated data directory (--data-dir)."}
	}
	if home != "" {
		if h, err := filepath.EvalSymlinks(home); err == nil && real == h {
			return &Error{Msg: "refusing to purge your home directory", Hint: "Use a dedicated data directory (--data-dir)."}
		}
	}
	// A symlinked layout directory could point anywhere.
	for _, d := range []string{p.ConfigDir, p.LogsDir, p.CacheDir, p.RunDir, p.RunnersDir} {
		if fi, err := os.Lstat(d); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return &Error{Msg: fmt.Sprintf("refusing to purge: %s is a symbolic link", d),
				Hint: "Replace it with a real directory or delete it by hand."}
		}
	}
	return nil
}

// within reports whether path is strictly inside root (lexically).
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// purge deletes this runner's files and, when no other runner remains, the
// shared ones. It never removes the data directory recursively: unknown files
// stay and are reported.
func (r *run) purge(ctx context.Context) purgeResult {
	p := r.d.Paths
	var res purgeResult

	if !r.noRunner {
		// The runner directory, with a container for root-owned files in _work.
		r.removeRunnerDir(ctx, &res)

		// Per-runner files.
		if matches, _ := filepath.Glob(filepath.Join(p.RunDir, r.name+".*")); len(matches) > 0 {
			for _, m := range matches {
				r.removeFile(m, &res)
			}
		}
		r.removeFile(p.RunnerLog(r.name), &res)
		r.removeFile(filepath.Join(p.LogsDir, "supervisor-"+r.name+".log"), &res)
	} else {
		r.removeOrphans(&res)
	}
	if cfg, err := config.Load(p); err != nil || cfg.Runner.Name == r.name {
		r.removeFile(p.ConfigFile, &res)
	}

	if len(runnerDirs(p)) > 0 {
		res.OthersRemain = true
		return res
	}

	// Shared files: only OwnCI's own, then each directory if it is empty.
	r.removeAll(p.CacheDir, &res)
	r.removeFile(p.OwnciLog, &res)
	for _, d := range []string{p.LogsDir, p.RunDir, p.RunnersDir, p.ConfigDir} {
		r.removeEmptyDir(d, &res)
	}
	// The marker goes last, and only when nothing else is in the way, so a
	// directory that still holds foreign files is never left unmarked.
	if es, err := os.ReadDir(p.Root); err == nil {
		var other []string
		for _, e := range es {
			if e.Name() != config.MarkerName {
				other = append(other, e.Name())
			}
		}
		if len(other) == 0 {
			r.removeFile(p.Marker, &res)
			if err := os.Remove(p.Root); err == nil {
				res.DataDirRemoved = true
			} else {
				res.Leftovers = append(res.Leftovers, p.Root)
			}
		} else {
			res.Hints = append(res.Hints, "the data directory still holds files ownci did not create; delete it by hand if you want it gone")
			for _, n := range other {
				res.Leftovers = appendUnique(res.Leftovers, filepath.Join(p.Root, n))
			}
		}
	}
	return res
}

// removeOrphans removes the per-runner files of runners whose directory is
// gone (deleted by hand). Only files of the known naming are touched, and a
// runner that still holds its lock is left alone.
func (r *run) removeOrphans(res *purgeResult) {
	p := r.d.Paths
	names := map[string]bool{}
	for _, g := range []struct{ dir, prefix, suffix string }{
		{p.RunDir, "", ".lock"},
		{p.RunDir, "", ".pid"},
		{p.RunDir, "", ".state.json"},
		{p.LogsDir, "runner-", ".log"},
		{p.LogsDir, "supervisor-", ".log"},
	} {
		matches, _ := filepath.Glob(filepath.Join(g.dir, g.prefix+"*"+g.suffix))
		for _, m := range matches {
			name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), g.prefix), g.suffix)
			if runner.ValidateName(name) == nil {
				names[name] = true
			}
		}
	}
	for name := range names {
		if running, _, _ := supervisor.IsRunning(p, name); running {
			res.Hints = append(res.Hints, fmt.Sprintf("runner '%s' is still running although its directory is gone; stop it and run uninstall again", name))
			continue
		}
		for _, f := range []string{p.LockFile(name), p.PidFile(name), p.StateFile(name), p.RunnerLog(name),
			filepath.Join(p.LogsDir, "supervisor-"+name+".log")} {
			r.removeFile(f, res)
		}
	}
}

func (r *run) removeRunnerDir(ctx context.Context, res *purgeResult) {
	dir := r.dir
	if !within(r.d.Paths.Root, dir) {
		return // cannot happen for a validated name; belt and braces
	}
	err := os.RemoveAll(dir)
	if err != nil && errors.Is(err, fs.ErrPermission) {
		// Job containers run as root, so _work can hold files we cannot delete.
		if cerr := r.cleanWorkWithContainer(ctx); cerr != nil {
			res.Hints = append(res.Hints, fmt.Sprintf("root-owned files in %s could not be removed (%v); run: sudo rm -rf %s",
				filepath.Join(dir, "_work"), cerr, dir))
			res.Leftovers = appendUnique(res.Leftovers, dir)
			return
		}
		err = os.RemoveAll(dir)
	}
	if err != nil {
		res.Hints = append(res.Hints, fmt.Sprintf("could not remove %s (%v); run: sudo rm -rf %s", dir, err, dir))
		res.Leftovers = appendUnique(res.Leftovers, dir)
	}
}

// cleanWorkWithContainer empties <runner>/_work in a throwaway container, the
// only way to delete files a root job container left behind.
func (r *run) cleanWorkWithContainer(ctx context.Context) error {
	work := filepath.Join(r.dir, "_work")
	if fi, err := os.Lstat(work); err != nil || !fi.IsDir() {
		return errors.New("no _work directory to clean")
	}
	if _, err := r.d.Exec.LookPath("docker"); err != nil {
		return errors.New("docker is not available")
	}
	res, err := r.d.Exec.Run(ctx, execx.Cmd{Name: "docker", Args: []string{
		"run", "--rm", "--pull", "never", "--network", "none", "--label", "ownci.managed=true",
		"-v", work + ":/w", hooks.CleanupImage,
		"sh", "-c", "rm -rf /w/* /w/.[!.]*",
	}})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(string(res.Stderr))
		if len(detail) > 200 {
			detail = detail[:200] + "…"
		}
		return fmt.Errorf("the cleanup container failed: %s", detail)
	}
	return nil
}

func (r *run) removeFile(path string, res *purgeResult) {
	if !within(r.d.Paths.Root, path) {
		return
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		res.Leftovers = appendUnique(res.Leftovers, path)
	}
}

func (r *run) removeAll(path string, res *purgeResult) {
	if !within(r.d.Paths.Root, path) {
		return
	}
	if err := os.RemoveAll(path); err != nil {
		res.Leftovers = appendUnique(res.Leftovers, path)
	}
}

// removeEmptyDir removes a layout directory only when nothing is left in it.
func (r *run) removeEmptyDir(path string, res *purgeResult) {
	if !within(r.d.Paths.Root, path) {
		return
	}
	err := os.Remove(path)
	switch {
	case err == nil, errors.Is(err, fs.ErrNotExist):
	default:
		if es, rerr := os.ReadDir(path); rerr == nil {
			for _, e := range es {
				res.Leftovers = appendUnique(res.Leftovers, filepath.Join(path, e.Name()))
			}
		} else {
			res.Leftovers = appendUnique(res.Leftovers, path)
		}
	}
}

func appendUnique(l []string, s string) []string {
	for _, x := range l {
		if x == s {
			return l
		}
	}
	return append(l, s)
}
