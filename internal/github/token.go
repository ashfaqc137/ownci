package github

import (
	"context"
	"strings"

	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/logging"
)

// TokenSource says where a resolved token came from.
type TokenSource string

const (
	SourceGitHubToken TokenSource = "GITHUB_TOKEN"
	SourceGHToken     TokenSource = "GH_TOKEN"
	SourceGH          TokenSource = "gh"
)

// ResolveToken finds a GitHub token in this order: $GITHUB_TOKEN,
// $GH_TOKEN, then `gh auth token`. The token is registered with the log
// redactor before it is returned. Nothing is persisted.
func ResolveToken(ctx context.Context, getenv func(string) string, r execx.Runner) (string, TokenSource, error) {
	for _, src := range []TokenSource{SourceGitHubToken, SourceGHToken} {
		if tok := strings.TrimSpace(getenv(string(src))); tok != "" {
			logging.RegisterSecret(tok)
			return tok, src, nil
		}
	}
	if _, err := r.LookPath("gh"); err == nil {
		res, err := r.Run(ctx, execx.Cmd{Name: "gh", Args: []string{"auth", "token"}})
		if err == nil && res.ExitCode == 0 {
			if tok := strings.TrimSpace(string(res.Stdout)); tok != "" {
				logging.RegisterSecret(tok)
				return tok, SourceGH, nil
			}
		}
	}
	return "", "", &Error{
		Msg:  "no GitHub token found",
		Hint: "Set GITHUB_TOKEN to a token with admin access to the repository, or run 'gh auth login'.",
	}
}
