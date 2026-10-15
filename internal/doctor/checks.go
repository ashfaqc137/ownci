package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/ashfaqc137/ownci/internal/docker"
	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/platform"
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
