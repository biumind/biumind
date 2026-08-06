package api

import "testing"

func TestValidRedirectURI(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://app.example.com/callback", true},
		{"http://127.0.0.1:55320/oauth", true},    // RFC 8252 loopback OK
		{"claude-desktop://oauth/callback", true}, // custom scheme OK
		{"", false},
		{"/relative/path", false},
		{"https://app.example.com/callback#frag", false}, // fragments banned
		{"app", false},
	}
	for _, c := range cases {
		got := validRedirectURI(c.in)
		if got != c.want {
			t.Errorf("validRedirectURI(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
