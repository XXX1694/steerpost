package user

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	got, err := NormalizeEmail("  Alice@Example.COM ")
	if err != nil || got != "alice@example.com" {
		t.Fatalf("got %q %v", got, err)
	}
	for _, bad := range []string{"", "nope", "a@", "Alice <a@b.c>"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Fatalf("expected error for %q", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if ValidatePassword("short") == nil {
		t.Fatal("short accepted")
	}
	if ValidatePassword(strings.Repeat("x", 129)) == nil {
		t.Fatal("long accepted")
	}
	if err := ValidatePassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}
}

func TestHasPassword(t *testing.T) {
	for hash, want := range map[string]bool{"": false, "$argon2id$v=19$m=1,t=1,p=1$a$b": true} {
		if got := (&User{PasswordHash: hash}).HasPassword(); got != want {
			t.Errorf("HasPassword(%q) = %v, want %v", hash, got, want)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{"bob@x.io": "b***@x.io", "@x.io": "***", "nope": "***", "": "***"} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
