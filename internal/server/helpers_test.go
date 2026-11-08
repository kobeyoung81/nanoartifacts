package server

import "testing"

func TestFilenameForIsHeaderSafe(t *testing.T) {
	cases := map[string]string{
		"":               "artifact.html",
		"Quarterly deck": "Quarterly deck.html",
		`bad "quotes"`:   "bad -quotes-.html",
		"a/b\\c":         "a-b-c.html",
		"tab\there":      "tab-here.html",
	}
	for input, want := range cases {
		if got := filenameFor(input); got != want {
			t.Fatalf("filenameFor(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWorkViewURLIsAPortalRoute(t *testing.T) {
	// The portal route carries the chrome and survives a hard navigation; the
	// service's own root does neither.
	if got := workViewURL("abc123"); got != "/artifacts?work=abc123" {
		t.Fatalf("workViewURL = %q", got)
	}
	if got := workViewURL("a/b"); got != "/artifacts?work=a%2Fb" {
		t.Fatalf("workViewURL escaped = %q", got)
	}
}

func TestFilenameForKeepsCJK(t *testing.T) {
	if got := filenameFor("季度复盘"); got != "季度复盘.html" {
		t.Fatalf("CJK title = %q", got)
	}
}
