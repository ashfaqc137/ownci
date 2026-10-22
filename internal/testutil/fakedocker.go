package testutil

import (
	"embed"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

//go:embed fakedocker/docker
var fakeDockerFS embed.FS

// FakeDocker is a fake `docker` executable placed first on PATH. See
// fakedocker/docker for what it answers and the env vars that steer it.
type FakeDocker struct {
	Dir string // state directory; holds image-present while the image "exists"
	Log string // every call's arguments, one line per call
}

// UseFakeDocker installs the fake for the duration of the test. The cleanup
// image starts out present. Child processes inherit PATH, so a supervisor
// re-executed by a test uses it too.
func UseFakeDocker(t testing.TB) FakeDocker {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := fakeDockerFS.ReadFile("fakedocker/docker")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	f := FakeDocker{Dir: filepath.Join(root, "state"), Log: filepath.Join(root, "calls.log")}
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_DIR", f.Dir)
	t.Setenv("FAKE_DOCKER_LOG", f.Log)
	f.SetImagePresent(true)
	return f
}

// SetImagePresent makes `docker image inspect` succeed or fail.
func (f FakeDocker) SetImagePresent(present bool) {
	marker := filepath.Join(f.Dir, "image-present")
	if present {
		_ = os.WriteFile(marker, nil, 0o644)
	} else {
		_ = os.Remove(marker)
	}
}

// Calls returns the recorded invocations, oldest first.
func (f FakeDocker) Calls() []string {
	b, err := os.ReadFile(f.Log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// SetContainer makes the container name exist (`inspect` prints content, e.g.
// "running <image>") or, with content "", not exist.
func (f FakeDocker) SetContainer(name, content string) {
	path := filepath.Join(f.Dir, "container-"+name)
	if content == "" {
		_ = os.Remove(path)
		return
	}
	_ = os.WriteFile(path, []byte(content+"\n"), 0o644)
}

// ContainerExists reports whether the fake container name exists.
func (f FakeDocker) ContainerExists(name string) bool {
	_, err := os.Stat(filepath.Join(f.Dir, "container-"+name))
	return err == nil
}

// CallsMatching returns the recorded invocations that start with prefix.
func (f FakeDocker) CallsMatching(prefix string) []string {
	var out []string
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}
