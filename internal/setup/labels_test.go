package setup

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestValidateLabels(t *testing.T) {
	got, err := ValidateLabels([]string{"gpu", " big-box ", "", "GPU", "ownci", "a.b_c-1"})
	if err != nil || !reflect.DeepEqual(got, []string{"gpu", "big-box", "a.b_c-1"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := ValidateLabels(nil); err != nil || len(got) != 0 {
		t.Errorf("nil: %v, %v", got, err)
	}
	for _, bad := range []string{"self-hosted", "Self-Hosted", "linux", "x64", "arm64", "macos", "ubuntu-latest", "Ubuntu-24.04", "windows-2025", "macos-15", "a b", "gpu!", "é"} {
		_, err := ValidateLabels([]string{"ok", bad})
		var e *Error
		if !errors.As(err, &e) || !e.Usage || e.Hint == "" {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
	many := make([]string, 11)
	for i := range many {
		many[i] = "l" + strings.Repeat("x", i)
	}
	if _, err := ValidateLabels(many); err == nil {
		t.Error("11 labels accepted")
	}
	if _, err := ValidateLabels(many[:10]); err != nil {
		t.Errorf("10 labels rejected: %v", err)
	}
}
