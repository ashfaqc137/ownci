package platform

import (
	"errors"
	"testing"
)

func TestDetectWith(t *testing.T) {
	tests := []struct {
		name       string
		osrelease  string // "" = file unreadable
		env        map[string]string
		goos, arch string
		wantWSL    bool
		wantRunner string
		wantKernel string
		supported  bool
	}{
		{"wsl osrelease amd64", "6.18.54.1-microsoft-standard-WSL2\n", nil, "linux", "amd64", true, "linux-x64", "6.18.54.1-microsoft-standard-WSL2", true},
		{"plain linux", "6.8.0-generic\n", nil, "linux", "amd64", false, "linux-x64", "6.8.0-generic", true},
		{"env-only wsl", "6.8.0-generic\n", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, "linux", "arm64", true, "linux-arm64", "6.8.0-generic", true},
		{"arm64", "6.8.0\n", nil, "linux", "arm64", false, "linux-arm64", "6.8.0", true},
		{"386 unsupported", "6.8.0\n", nil, "linux", "386", false, "", "6.8.0", false},
		{"darwin unsupported", "", nil, "darwin", "arm64", false, "", "", false},
		{"unreadable osrelease", "", nil, "linux", "amd64", false, "linux-x64", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			read := func(p string) ([]byte, error) {
				if p != osReleasePath || tc.osrelease == "" {
					return nil, errors.New("nope")
				}
				return []byte(tc.osrelease), nil
			}
			getenv := func(k string) string { return tc.env[k] }
			info := DetectWith(read, getenv, tc.goos, tc.arch)
			if info.IsWSL != tc.wantWSL || info.RunnerArch != tc.wantRunner || info.KernelRelease != tc.wantKernel {
				t.Fatalf("got %+v", info)
			}
			if tc.env["WSL_DISTRO_NAME"] != info.WSLDistro {
				t.Errorf("WSLDistro = %q", info.WSLDistro)
			}
			if err := info.Supported(); (err == nil) != tc.supported {
				t.Errorf("Supported() = %v, want supported=%v", err, tc.supported)
			}
		})
	}
}

func TestIsWindowsMount(t *testing.T) {
	for path, want := range map[string]bool{
		"/mnt/c/x":          true,
		"/mnt/D/x/y":        true,
		"/mnt/c":            true,
		"/mnt/wsl/x":        false,
		"/mnt/":             false,
		"/home/x":           false,
		"/mnt/c/../../home": false,
	} {
		if got := IsWindowsMount(path); got != want {
			t.Errorf("IsWindowsMount(%q) = %v, want %v", path, got, want)
		}
	}
}
