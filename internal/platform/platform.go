// Package platform detects the OS, CPU architecture and WSL environment.
package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Info describes the machine ownci runs on.
type Info struct {
	OS, Arch      string // runtime.GOOS / GOARCH
	RunnerArch    string // "linux-x64" | "linux-arm64" | "" when unsupported
	IsWSL         bool
	WSLDistro     string // from $WSL_DISTRO_NAME, may be empty
	KernelRelease string
}

const osReleasePath = "/proc/sys/kernel/osrelease"

// Detect inspects the real /proc, environment and runtime.
func Detect() Info {
	return DetectWith(os.ReadFile, os.Getenv, runtime.GOOS, runtime.GOARCH)
}

// DetectWith is Detect with its inputs injected, for tests.
func DetectWith(readFile func(string) ([]byte, error), getenv func(string) string, goos, goarch string) Info {
	info := Info{OS: goos, Arch: goarch, WSLDistro: getenv("WSL_DISTRO_NAME")}
	if goos == "linux" {
		switch goarch {
		case "amd64":
			info.RunnerArch = "linux-x64"
		case "arm64":
			info.RunnerArch = "linux-arm64"
		}
		if b, err := readFile(osReleasePath); err == nil {
			info.KernelRelease = strings.TrimSpace(string(b))
		}
	}
	info.IsWSL = info.WSLDistro != "" || strings.Contains(strings.ToLower(info.KernelRelease), "microsoft")
	return info
}

// Supported reports whether the platform can host a runner (D9).
func (i Info) Supported() error {
	if i.OS != "linux" {
		return fmt.Errorf("unsupported platform %s/%s: ownci supports Linux (including WSL2) only", i.OS, i.Arch)
	}
	if i.RunnerArch == "" {
		return fmt.Errorf("unsupported architecture %q: ownci supports amd64 and arm64", i.Arch)
	}
	return nil
}

// IsWindowsMount reports whether path is on a WSL drvfs mount (/mnt/<letter>/...).
func IsWindowsMount(path string) bool {
	rest, ok := strings.CutPrefix(filepath.Clean(path), "/mnt/")
	if !ok || rest == "" {
		return false
	}
	drive, _, _ := strings.Cut(rest, "/")
	if len(drive) != 1 {
		return false
	}
	c := drive[0] | 0x20
	return c >= 'a' && c <= 'z'
}
