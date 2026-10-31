package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ashfaqc137/ownci/internal/docker"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/isolation"
	"github.com/ashfaqc137/ownci/internal/platform"
	"github.com/ashfaqc137/ownci/internal/runnercontainer"
)

// PlatformCheck verifies the OS and CPU architecture are supported.
func PlatformCheck(info platform.Info) Check {
	return Check{Name: "platform", Run: func(context.Context) Result {
		if err := info.Supported(); err != nil {
			return Result{Level: Fail, Detail: err.Error(), Hint: "OwnCI supports Linux (including WSL2) on amd64 and arm64."}
		}
		return Result{Level: Pass, Detail: fmt.Sprintf("%s/%s → runner %s", info.OS, info.Arch, info.RunnerArch)}
	}}
}

// WSLCheck is informational: it reports whether ownci runs under WSL.
func WSLCheck(info platform.Info) Check {
	return Check{Name: "wsl", Run: func(context.Context) Result {
		if !info.IsWSL {
			return Result{Level: Pass, Detail: "not running under WSL"}
		}
		kind := "WSL"
		if strings.Contains(strings.ToLower(info.KernelRelease), "wsl2") {
			kind = "WSL2"
		}
		if info.WSLDistro != "" {
			kind += " (" + info.WSLDistro + ")"
		}
		return Result{Level: Pass, Detail: kind}
	}}
}

// UserCheck fails when running as root, which the runner refuses.
func UserCheck(geteuid func() int) Check {
	return Check{Name: "user", Run: func(context.Context) Result {
		if geteuid() == 0 {
			return Result{Level: Fail, Detail: "running as root", Hint: "The runner refuses to run as root; run ownci as a normal user."}
		}
		return Result{Level: Pass, Detail: fmt.Sprintf("running as uid %d", geteuid())}
	}}
}

// DataDirCheck verifies the data directory is writable and on a Linux filesystem.
func DataDirCheck(dir string) Check {
	return Check{Name: "data-dir", Run: func(context.Context) Result {
		f, err := os.CreateTemp(dir, ".doctor-*")
		if err != nil {
			return Result{Level: Fail, Detail: fmt.Sprintf("%s is not writable: %v", dir, err),
				Hint: "Fix the directory permissions or choose another with --data-dir."}
		}
		f.Close()
		os.Remove(f.Name())
		if platform.IsWindowsMount(dir) {
			return Result{Level: Warn, Detail: dir + " is on a Windows mount (/mnt/<drive>)",
				Hint: "Windows mounts are slow and have permission quirks; use a Linux path such as ~/.local/share/ownci (--data-dir)."}
		}
		return Result{Level: Pass, Detail: dir}
	}}
}

// ldconfigCandidates: ldconfig usually lives in /sbin, which is often not on a
// normal user's PATH.
var ldconfigCandidates = []string{"ldconfig", "/sbin/ldconfig", "/usr/sbin/ldconfig"}

// RunnerDepsCheck looks for ICU, which the .NET-based runner needs.
func RunnerDepsCheck(r execx.Runner) Check {
	return Check{Name: "runner-deps", Run: func(ctx context.Context) Result {
		var path string
		for _, c := range ldconfigCandidates {
			if p, err := r.LookPath(c); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			return Result{Level: Warn, Detail: "ldconfig not found; cannot verify the ICU library",
				Hint: "If the runner fails to start, install ICU or run bin/installdependencies.sh from the runner directory."}
		}
		res, err := r.Run(ctx, execx.Cmd{Name: path, Args: []string{"-p"}})
		if err != nil || res.ExitCode != 0 {
			return Result{Level: Warn, Detail: "ldconfig -p failed; cannot verify the ICU library",
				Hint: "If the runner fails to start, install ICU or run bin/installdependencies.sh from the runner directory."}
		}
		if strings.Contains(string(res.Stdout), "libicuuc") {
			return Result{Level: Pass, Detail: "ICU library found"}
		}
		return Result{Level: Fail, Detail: "ICU library (libicuuc) not found",
			Hint: "Install ICU: Debian/Ubuntu: sudo apt-get install -y libicu-dev; Fedora: sudo dnf install libicu; openSUSE: sudo zypper install libicu. Or run bin/installdependencies.sh after the runner is downloaded."}
	}}
}

// DockerCheck verifies Docker is installed, reachable and in Linux mode.
func DockerCheck(r execx.Runner) Check {
	return Check{Name: "docker", Run: func(ctx context.Context) Result {
		st := docker.Check(ctx, r)
		if st.OK() {
			return Result{Level: Pass, Detail: fmt.Sprintf("%s (%s)", st.ServerVersion, st.ServerOS)}
		}
		res := Result{Level: Fail, Detail: st.Err.Error()}
		var de *docker.Error
		if errors.As(st.Err, &de) {
			res.Hint = de.Hint
		}
		return res
	}}
}

// GitCheck warns when git is missing on the host; container jobs use the
// job container's git, so this is not fatal.
func GitCheck(r execx.Runner) Check {
	return Check{Name: "git", Run: func(context.Context) Result {
		p, err := r.LookPath("git")
		if err != nil {
			return Result{Level: Warn, Detail: "git not found on host (not required for container jobs; useful for troubleshooting)",
				Hint: "Install git if you want to debug checkouts on the host."}
		}
		return Result{Level: Pass, Detail: p}
	}}
}

// IsolationCheck verifies the runner enforces its isolation policy: in host
// mode job containers are required; in both modes the cleanup hook and
// cleanup image are in place.
func IsolationCheck(r execx.Runner, runnerDir string, p isolation.Policy) Check {
	return Check{Name: "isolation", Run: func(ctx context.Context) Result {
		rep := isolation.Verify(ctx, r, runnerDir, p)
		switch {
		case !rep.EnforcementOK():
			return Result{Level: Fail, Detail: strings.Join(rep.Problems(), "; "),
				Hint: "'ownci start' repairs this, or run 'ownci setup --force'."}
		case !rep.CleanupImage.OK:
			return Result{Level: Warn, Detail: rep.CleanupImage.Detail,
				Hint: "docker pull " + p.CleanupImage + " ('ownci start' also pulls it)."}
		}
		what := "container jobs required"
		if rep.JobContainerRequired == nil {
			what = "runner runs in a container"
		}
		detail := what + ", cleanup hook and image in place; last cleanup: " + rep.LastCleanup.Result
		if rep.LastCleanup.Result == "failed" {
			return Result{Level: Warn, Detail: detail, Hint: "The last per-job cleanup failed: " + rep.LastCleanup.Detail}
		}
		return Result{Level: Pass, Detail: detail}
	}}
}

// RunnerContainerCheck verifies what container-runner mode needs: a
// runner directory Docker can mount at the same path, and a runner image that
// is present and has the runner's dependencies.
func RunnerContainerCheck(r execx.Runner, runnerDir, image string) Check {
	return Check{Name: "runner-container", Run: func(ctx context.Context) Result {
		if err := runnercontainer.CheckPath(runnerDir); err != nil {
			return Result{Level: Fail, Detail: err.Error(), Hint: hintOf(err)}
		}
		if !docker.ImagePresent(ctx, r, image) {
			return Result{Level: Warn, Detail: "runner image " + image + " is not present locally",
				Hint: "docker pull " + image + " ('ownci start' also pulls it)."}
		}
		warnings, err := runnercontainer.Probe(ctx, r, image)
		if err != nil {
			return Result{Level: Fail, Detail: err.Error(), Hint: hintOf(err)}
		}
		if len(warnings) > 0 {
			return Result{Level: Warn, Detail: "image " + image + " can host the runner, but " + strings.Join(warnings, "; "),
				Hint: "Add the missing tools to the image if your workflows need them."}
		}
		return Result{Level: Pass, Detail: "image " + image + " can host the runner; " + runnerDir + " is mountable"}
	}}
}

func hintOf(err error) string {
	var h interface{ UserHint() string }
	if errors.As(err, &h) {
		return h.UserHint()
	}
	return ""
}
