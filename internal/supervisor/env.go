package supervisor

import "strings"

// tokenVars are the variables OwnCI reads a GitHub token from. The runner and
// the background supervisor never need them, and jobs must not inherit them.
var tokenVars = []string{"GITHUB_TOKEN", "GH_TOKEN"}

// WithoutTokens returns env without the GitHub token variables.
func WithoutTokens(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, t := range tokenVars {
			if name == t {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}
