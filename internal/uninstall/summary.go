package uninstall

import (
	"fmt"

	"github.com/ashfaqc137/ownci/internal/hooks"
)

// summary is what `uninstall` reports at the end, truthfully for each part.
type summary struct {
	process string
	github  string
	files   purgeResult
}

func (r *run) finishSummary(unregErr error) {
	o := r.o
	if r.sum.github == "" {
		switch {
		case o.LocalOnly:
			r.sum.github = "skipped (--local-only)"
		case unregErr != nil:
			r.sum.github = "still registered (unregistering failed; remove at " + r.settingsURL() + ")"
		case r.registered:
			r.sum.github = "still registered (remove at " + r.settingsURL() + ")"
		default:
			r.sum.github = "not registered locally (nothing was registered by this install)"
		}
	}
}

func (r *run) printSummary() {
	w := r.d.Out
	proc := r.sum.process
	if proc == "" {
		proc = "was not running"
	}
	fmt.Fprintln(w, "\nUninstall summary")
	fmt.Fprintf(w, "  %-16s %s\n", "Runner process", proc)
	fmt.Fprintf(w, "  %-16s %s\n", "GitHub", r.sum.github)
	if r.o.Purge {
		fmt.Fprintf(w, "  %-16s %s\n", "Local files", r.sum.files.describe(r.d.Paths.Root))
		for _, l := range r.sum.files.Leftovers {
			fmt.Fprintf(w, "  %-16s %s\n", "", "left: "+l)
		}
		for _, h := range r.sum.files.Hints {
			fmt.Fprintf(w, "  %-16s %s\n", "", h)
		}
	} else {
		fmt.Fprintf(w, "  %-16s kept at %s (use --purge to remove)\n", "Local files", r.d.Paths.Root)
	}
	images := hooks.CleanupImage
	if r.cfg != nil && r.cfg.Image != "" {
		images += " " + r.cfg.Image
	}
	fmt.Fprintf(w, "  %-16s Docker images: the jobs' images, and OwnCI's (remove with 'docker image rm %s' if desired)\n", "Not touched", images)
	if r.o.Purge && !r.sum.files.OthersRemain {
		r.printBinaryHint()
	}
}

// printBinaryHint says how to remove the CLI itself. ownci never deletes its
// own binary: it may be a dev build or belong to a package manager.
func (r *run) printBinaryHint() {
	if r.d.Executable == "" {
		return
	}
	fmt.Fprintf(r.d.Out, "\nTo remove the ownci CLI itself: rm %s\n", r.d.Executable)
}
