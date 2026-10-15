// Package github is OwnCI's small GitHub REST client: repository parsing,
// token resolution and exactly the endpoints OwnCI needs.
package github

import (
	"fmt"
	"strings"
)

// Repo identifies a GitHub repository.
type Repo struct{ Owner, Name string }

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// ParseRepo accepts "owner/name", "https://github.com/owner/name[.git][/]" and
// "git@github.com:owner/name.git".
func ParseRepo(s string) (Repo, error) {
	orig := s
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "https://github.com/"):
		s = strings.TrimPrefix(s, "https://github.com/")
	case strings.HasPrefix(s, "git@github.com:"):
		s = strings.TrimPrefix(s, "git@github.com:")
	}
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")

	owner, name, ok := strings.Cut(s, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") ||
		!validPart(owner) || !validPart(name) || name == "." || name == ".." {
		return Repo{}, &Error{
			Msg:  fmt.Sprintf("invalid repository %q", orig),
			Hint: "Use owner/name, e.g. octocat/hello-world, or the repository's GitHub URL.",
		}
	}
	return Repo{Owner: owner, Name: name}, nil
}

func validPart(s string) bool {
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}
