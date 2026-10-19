package core

import "testing"

func TestGenerateMemberPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw, err := GenerateMemberPassword()
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != memberPwLen {
			t.Fatalf("password length = %d, want %d", len(pw), memberPwLen)
		}
		for _, r := range pw {
			if !containsRune(memberPwAlphabet, r) {
				t.Fatalf("password %q has out-of-alphabet rune %q", pw, r)
			}
		}
		seen[pw] = true
	}
	if len(seen) < 190 {
		t.Fatalf("passwords not random enough: %d distinct of 200", len(seen))
	}
}

func TestHashVerifyPassword(t *testing.T) {
	h1, err := HashPassword("ABCD2345")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h1, "ABCD2345") {
		t.Fatal("correct password should verify")
	}
	if VerifyPassword(h1, "abcd2345") {
		t.Fatal("wrong password must not verify")
	}
	// Same password hashed twice must differ (random salt).
	h2, _ := HashPassword("ABCD2345")
	if h1 == h2 {
		t.Fatal("two hashes of the same password must differ (salt)")
	}
	// Malformed encodings never verify.
	for _, bad := range []string{"", "nope", "pbkdf2_sha256$x$y$z", "pbkdf2_sha256$1000$@@@$@@@"} {
		if VerifyPassword(bad, "ABCD2345") {
			t.Fatalf("malformed hash %q should not verify", bad)
		}
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
