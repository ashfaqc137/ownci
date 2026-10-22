package docker

import (
	"context"
	"fmt"
	"time"

	"github.com/ashfaqc137/ownci/internal/execx"
)

const (
	pullTimeout    = 20 * time.Minute // the runner image is ~2 GB
	inspectTimeout = 15 * time.Second
)

// ImagePresent reports whether image is in the local image store.
func ImagePresent(ctx context.Context, ex execx.Runner, image string) bool {
	ctx, cancel := context.WithTimeout(ctx, inspectTimeout)
	defer cancel()
	res, err := ex.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"image", "inspect", "--format", "{{.Id}}", image}})
	return err == nil && res.ExitCode == 0
}

// Pull pulls image. The error carries the end of Docker's message and a hint.
func Pull(ctx context.Context, ex execx.Runner, image string) error {
	ctx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()
	res, err := ex.Run(ctx, execx.Cmd{Name: "docker", Args: []string{"pull", "--quiet", image}})
	if err != nil || res.ExitCode != 0 {
		detail := fmt.Sprint(err)
		if err == nil {
			detail = excerpt(string(res.Stderr))
		}
		return &Error{
			Msg:  fmt.Sprintf("pulling the image %s failed: %s", image, detail),
			Hint: "Check your network and Docker (run 'ownci doctor'), then retry.",
		}
	}
	return nil
}

// EnsureImage pulls image unless it is already present. It reports whether
// it pulled.
func EnsureImage(ctx context.Context, ex execx.Runner, image string) (pulled bool, err error) {
	if ImagePresent(ctx, ex, image) {
		return false, nil
	}
	return true, Pull(ctx, ex, image)
}
