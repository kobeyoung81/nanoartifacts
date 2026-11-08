package sign

import (
	"strings"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	s := New("secret", time.Hour)
	token, exp := s.Session("user-1")

	got, err := s.VerifySession(token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != "user-1" {
		t.Fatalf("subject = %q", got)
	}
	if time.Until(exp) <= 0 {
		t.Fatalf("expiry is in the past: %v", exp)
	}
}

func TestSessionRejectsTamperedSubject(t *testing.T) {
	s := New("secret", time.Hour)
	token, _ := s.Session("user-1")

	// Swap the encoded subject, keep the signature: the mac covers the payload,
	// so this must fail rather than authenticate as user-2.
	parts := strings.Split(token, ".")
	tampered := "dXNlci0y" + "." + parts[1] + "." + parts[2]
	if _, err := s.VerifySession(tampered); err == nil {
		t.Fatal("a tampered subject was accepted")
	}
}

func TestSessionRejectsAnotherSecret(t *testing.T) {
	token, _ := New("secret", time.Hour).Session("user-1")
	if _, err := New("other", time.Hour).VerifySession(token); err == nil {
		t.Fatal("a token signed with another secret was accepted")
	}
}

func TestSessionRejectsExpired(t *testing.T) {
	// Expiry has second granularity (the payload stores a Unix timestamp), so
	// the shortest meaningful TTL is one second and the wait has to cross a
	// whole second boundary.
	s := New("secret", time.Second)
	token, _ := s.Session("user-1")
	time.Sleep(1100 * time.Millisecond)
	if _, err := s.VerifySession(token); err == nil {
		t.Fatal("an expired token was accepted")
	}
}

func TestSessionRejectsMalformed(t *testing.T) {
	s := New("secret", time.Hour)
	for _, token := range []string{"", "a.b", "a.b.c.d", "not-a-token"} {
		if _, err := s.VerifySession(token); err == nil {
			t.Fatalf("malformed token %q was accepted", token)
		}
	}
}
