package github

import (
	"fmt"
	"strings"
	"time"
)

// Error is a failure with an actionable hint. The cli layer renders it as
// "Error: …/Hint: …" through UserHint (this package must not import cli).
type Error struct {
	Msg  string
	Hint string
	Err  error // optional cause, for errors.Is
}

func (e *Error) Error() string    { return e.Msg }
func (e *Error) Unwrap() error    { return e.Err }
func (e *Error) UserHint() string { return e.Hint }

// APIError is a non-2xx response from the GitHub API. Message and DocURL are
// GitHub's own words; Error() is a friendlier summary. Neither ever contains
// the token.
type APIError struct {
	Status  int
	Message string
	DocURL  string

	msg  string
	hint string
}

func (e *APIError) Error() string    { return e.msg }
func (e *APIError) UserHint() string { return e.hint }

// newAPIError maps a failed response to a user-facing error (spec: t05).
// repo is "owner/name" or "" when the request was not repo-scoped.
func newAPIError(status int, message, docURL, repo string, src TokenSource, rateRemaining, rateReset string) *APIError {
	e := &APIError{Status: status, Message: message, DocURL: docURL}
	switch {
	case status == 401:
		e.msg = "GitHub token is invalid or expired"
		e.hint = tokenHint(src)
	case status == 403 && rateRemaining == "0":
		e.msg = "GitHub API rate limit exceeded"
		e.hint = "Wait a while and try again."
		if reset := parseReset(rateReset); !reset.IsZero() {
			e.hint = "Try again after " + reset.Local().Format("15:04:05") + "."
		}
	case status == 403:
		e.msg = fmt.Sprintf("the GitHub token lacks permission: admin access to %s is required", orRepo(repo))
		e.hint = "Use a fine-grained PAT with 'Administration: read & write' on this repository, or a classic PAT with the 'repo' scope."
	case status == 404:
		e.msg = "repository not found or token cannot see it"
		e.hint = "Check the owner/name, and that the token has access to the repository."
	default:
		e.msg = fmt.Sprintf("GitHub API error (HTTP %d): %s", status, message)
	}
	return e
}

func orRepo(repo string) string {
	if repo == "" {
		return "the repository"
	}
	return repo
}

func tokenHint(src TokenSource) string {
	switch src {
	case SourceGitHubToken:
		return "The token from $GITHUB_TOKEN was rejected. Create a new token, or unset GITHUB_TOKEN to fall back to 'gh auth login'."
	case SourceGHToken:
		return "The token from $GH_TOKEN was rejected. Create a new token, or unset GH_TOKEN to fall back to 'gh auth login'."
	case SourceGH:
		return "The token from 'gh auth token' was rejected. Run 'gh auth login' again, or set GITHUB_TOKEN."
	}
	return "Set GITHUB_TOKEN to a valid token, or run 'gh auth login'."
}

func parseReset(s string) time.Time {
	var sec int64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &sec); err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}
