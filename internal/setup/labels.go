package setup

import (
	"fmt"
	"regexp"
	"strings"
)

const maxExtraLabels = 10

var labelRE = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// reservedLabels are labels GitHub assigns to self-hosted runners by default
// (and the common OS/arch ones). Allowing them would let ordinary
// `runs-on: [self-hosted, linux]` workflows land here, defeating the opt-in label.
var reservedLabels = map[string]bool{
	"self-hosted": true, "linux": true, "windows": true, "macos": true,
	"x64": true, "x86": true, "arm": true, "arm64": true, "ubuntu": true,
}

// hostedPrefixes start the labels of GitHub-hosted runner images
// (ubuntu-latest, windows-2025, macos-15, ...). A runner carrying one could
// pick up workflows written for hosted runners.
var hostedPrefixes = []string{"ubuntu-", "windows-", "macos-"}

func reservedLabel(low string) bool {
	if reservedLabels[low] {
		return true
	}
	for _, p := range hostedPrefixes {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return false
}

// ValidateLabels checks the extra labels and returns them cleaned (trimmed,
// de-duplicated, without "ownci", which is always added).
func ValidateLabels(extra []string) ([]string, error) {
	var out []string
	seen := map[string]bool{"ownci": true}
	for _, l := range extra {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		low := strings.ToLower(l)
		switch {
		case !labelRE.MatchString(l):
			return nil, &Error{Usage: true, Msg: fmt.Sprintf("invalid label %q", l), Hint: "Labels may contain only letters, digits, '.', '_' and '-'."}
		case reservedLabel(low):
			return nil, &Error{Usage: true,
				Msg:  fmt.Sprintf("label %q is not allowed", l),
				Hint: "OwnCI registers with --no-default-labels so only 'runs-on: [ownci]' workflows reach this machine; GitHub's default and hosted-runner labels (ubuntu-*, windows-*, macos-*) would defeat that.",
			}
		}
		if seen[low] {
			continue
		}
		seen[low] = true
		out = append(out, l)
	}
	if len(out) > maxExtraLabels {
		return nil, &Error{Usage: true, Msg: fmt.Sprintf("too many labels (%d, max %d)", len(out), maxExtraLabels), Hint: "Pass fewer --labels."}
	}
	return out, nil
}
