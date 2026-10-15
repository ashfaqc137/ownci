// Command fakegh serves a fake GitHub API for driving a dev build of ownci
// (make build-dev) end to end without a GitHub account. Never shipped.
//
//	go run ./tools/fakegh [--addr 127.0.0.1:8787] [--state /tmp/fakegh.json]
//	eval "$(go run ./tools/fakegh --print-env)"
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ashfaqc137/ownci/internal/testutil/fakegh"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fakegh:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:8787", "listen address")
	state := flag.String("state", "", "JSON file to persist state in (default: memory only)")
	public := flag.String("public-repos", "", "comma-separated owner/name repos reported as public")
	noAdmin := flag.String("no-admin", "", "comma-separated owner/name repos where the token has no admin access")
	printEnv := flag.Bool("print-env", false, "print the export lines for --addr and exit")
	flag.Parse()

	if *printEnv {
		fmt.Println(strings.Join(fakegh.EnvLines("http://"+*addr), "\n"))
		return nil
	}

	srv, err := fakegh.Open(fakegh.Options{
		PublicRepos: split(*public),
		NoAdmin:     split(*noAdmin),
		StatePath:   *state,
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	base := "http://" + ln.Addr().String()
	fmt.Printf("fakegh listening on %s\nPaste into your shell (or: eval \"$(go run ./tools/fakegh --print-env)\"):\n", base)
	fmt.Println(strings.Join(fakegh.EnvLines(base), "\n"))

	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
	}()
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func split(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
