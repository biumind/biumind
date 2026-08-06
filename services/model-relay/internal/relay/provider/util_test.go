package provider

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"https://api.openai.com", "https://api.openai.com"},
		{"https://api.openai.com/", "https://api.openai.com"},
		{"https://api.openai.com/v1", "https://api.openai.com"},
		{"https://api.openai.com/v1/", "https://api.openai.com"},
		{"https://api.openai.com/v1//", "https://api.openai.com"},
		{"https://new-api.example.com/v1", "https://new-api.example.com"},
		{"https://litellm.local", "https://litellm.local"},
		{"https://proxy.example.com/api/v1", "https://proxy.example.com/api"},
		{"https://host/v1/extra", "https://host/v1/extra"}, // /v1 不在尾部, 保留
		{"https://host/v2", "https://host/v2"},             // 只剥 /v1, 不剥 /v2
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := NormalizeBaseURL(tc.in); got != tc.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
