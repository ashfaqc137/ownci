package github

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ashfaqc137/ownci/internal/execx"
	"github.com/ashfaqc137/ownci/internal/logging"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveTokenPrecedence(t *testing.T) {
	f := execx.NewFake()
	tok, src, err := ResolveToken(context.Background(), env(map[string]string{"GITHUB_TOKEN": " ghp-aaaaaaaa ", "GH_TOKEN": "ghp-bbbbbbbb"}), f)
	if err != nil || tok != "ghp-aaaaaaaa" || src != SourceGitHubToken {
		t.Fatalf("got %q %q %v", tok, src, err)
	}
	tok, src, err = ResolveToken(context.Background(), env(map[string]string{"GH_TOKEN": "ghp-bbbbbbbb"}), f)
	if err != nil || tok != "ghp-bbbbbbbb" || src != SourceGHToken {
		t.Fatalf("got %q %q %v", tok, src, err)
	}
	if len(f.Calls()) != 0 {
		t.Errorf("gh must not run when an env token exists: %v", f.Calls())
	}
}

func TestResolveTokenGHFallback(t *testing.T) {
	f := execx.NewFake()
	f.SetPath("gh", "/usr/bin/gh")
	f.On("gh", "auth", "token").Stdout("gho_fromgh12345678\n")
	tok, src, err := ResolveToken(context.Background(), env(nil), f)
	if err != nil || tok != "gho_fromgh12345678" || src != SourceGH {
		t.Fatalf("got %q %q %v", tok, src, err)
	}
}

func TestResolveTokenNothingFound(t *testing.T) {
	cases := map[string]func() execx.Runner{
		"gh missing": func() execx.Runner { return execx.NewFake() },
		"not logged in": func() execx.Runner {
			f := execx.NewFake()
			f.SetPath("gh", "/usr/bin/gh")
			f.On("gh", "auth", "token").Exit(1).Stderr("not logged in")
			return f
		},
		"empty output": func() execx.Runner {
			f := execx.NewFake()
			f.SetPath("gh", "/usr/bin/gh")
			f.On("gh", "auth", "token").Stdout("\n")
			return f
		},
		"gh fails to start": func() execx.Runner {
			f := execx.NewFake()
			f.SetPath("gh", "/usr/bin/gh")
			f.On("gh", "auth", "token").Fail(errors.New("boom"))
			return f
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := ResolveToken(context.Background(), env(nil), mk())
			var ge *Error
			if !errors.As(err, &ge) || !strings.Contains(ge.UserHint(), "set GITHUB_TOKEN") || !strings.Contains(ge.UserHint(), "gh auth login") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestResolveTokenRegistersSecret(t *testing.T) {
	const secret = "ghx-registered-secret-0001"
	if _, _, err := ResolveToken(context.Background(), env(map[string]string{"GITHUB_TOKEN": secret}), execx.NewFake()); err != nil {
		t.Fatal(err)
	}
	if got := logging.Redact("token is " + secret); strings.Contains(got, secret) {
		t.Errorf("secret not redacted: %q", got)
	}
	f := execx.NewFake()
	f.SetPath("gh", "/usr/bin/gh")
	const ghSecret = "ghx-from-gh-cli-secret-0002"
	f.On("gh", "auth", "token").Stdout(ghSecret)
	if _, _, err := ResolveToken(context.Background(), env(nil), f); err != nil {
		t.Fatal(err)
	}
	if got := logging.Redact(ghSecret); strings.Contains(got, ghSecret) {
		t.Errorf("gh secret not redacted: %q", got)
	}
}
