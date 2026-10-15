package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/ashfaqc137/ownci/internal/buildinfo"
)

// devWarning returns the one-line warning for dev builds with OWNCI_DEV_*
// overrides set (D8), or "" when there is nothing to warn about.
func devWarning(devBuild bool, environ []string) string {
	if !devBuild {
		return ""
	}
	var names []string
	for _, kv := range environ {
		name, val, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "OWNCI_DEV_") && val != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return "DEV BUILD: overrides active (" + strings.Join(names, ", ") + ")"
}

// printDevWarning writes the warning to w, once per invocation.
func printDevWarning(w io.Writer) {
	if msg := devWarning(buildinfo.DevBuild, os.Environ()); msg != "" {
		fmt.Fprintln(w, msg)
	}
}
