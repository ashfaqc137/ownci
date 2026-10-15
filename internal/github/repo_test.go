package github

import "testing"

func TestParseRepo(t *testing.T) {
	ok := map[string]Repo{
		"octo/hello":                        {"octo", "hello"},
		"https://github.com/octo/hello":     {"octo", "hello"},
		"https://github.com/octo/hello.git": {"octo", "hello"},
		"https://github.com/octo/hello/":    {"octo", "hello"},
		"git@github.com:octo/hello.git":     {"octo", "hello"},
		"a-b_c/d.e-f":                       {"a-b_c", "d.e-f"},
		"  octo/hello  ":                    {"octo", "hello"},
	}
	for in, want := range ok {
		got, err := ParseRepo(in)
		if err != nil || got != want {
			t.Errorf("ParseRepo(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if got := (Repo{"o", "n"}).String(); got != "o/n" {
		t.Errorf("String = %q", got)
	}
	for _, in := range []string{"", "owner", "a/b/c", "owner/na me", "/name", "owner/", "ow$ner/x", "owner/..", "https://gitlab.com/a/b"} {
		if r, err := ParseRepo(in); err == nil {
			t.Errorf("ParseRepo(%q) = %v, want error", in, r)
		}
	}
}
