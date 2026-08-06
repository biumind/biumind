package rss

import "testing"

func TestWeChatFeedURL(t *testing.T) {
	cases := []struct {
		name, relay, account, want string
		wantErr                    bool
	}{
		{"basic", "https://werss.app/feed/biumind", "卓克科技参考", "https://werss.app/feed/biumind/%E5%8D%93%E5%85%8B%E7%A7%91%E6%8A%80%E5%8F%82%E8%80%83", false},
		{"trailing slash", "https://werss.app/feed/biumind/", "abc", "https://werss.app/feed/biumind/abc", false},
		{"ascii name", "https://r.example.com", "TechCrunch", "https://r.example.com/TechCrunch", false},
		{"empty relay", "", "abc", "", true},
		{"empty name", "https://werss.app", "", "", true},
		{"bad relay", "not-a-url", "abc", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := WeChatFeedURL(c.relay, c.account)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestNitterFeedURL(t *testing.T) {
	cases := []struct {
		name, instance, handle, want string
		wantErr                      bool
	}{
		{"plain handle", "https://nitter.net", "elonmusk", "https://nitter.net/elonmusk/rss", false},
		{"@ prefix stripped", "https://nitter.net", "@elonmusk", "https://nitter.net/elonmusk/rss", false},
		{"trailing slash instance", "https://n.example.com/", "jack", "https://n.example.com/jack/rss", false},
		{"empty instance disables", "", "elonmusk", "", true},
		{"empty handle", "https://nitter.net", "", "", true},
		{"bad instance", "nope", "x", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NitterFeedURL(c.instance, c.handle)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestTwitterHandleFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://x.com/elonmusk", "elonmusk"},
		{"https://twitter.com/jack", "jack"},
		{"https://www.x.com/Naval/", "Naval"},
		{"https://mobile.twitter.com/dhh", "dhh"},
		{"https://x.com/home", ""},          // reserved path
		{"https://x.com/i/lists/123", ""},   // not a profile URL
		{"https://example.com/elonmusk", ""}, // wrong host
		{"elonmusk", ""},                    // not a URL
	}
	for _, c := range cases {
		if got := TwitterHandleFromURL(c.in); got != c.want {
			t.Errorf("TwitterHandleFromURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
