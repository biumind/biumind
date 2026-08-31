package sources

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestNormalizeCreateReq(t *testing.T) {
	hashHex := hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

	cases := []struct {
		name         string
		req          createReq
		wantErr      string
		wantText     string
		wantHashLen  int
		wantMetaKeys []string
	}{
		{
			name:    "missing rel_path",
			req:     createReq{RawText: "x"},
			wantErr: "missing_rel_path",
		},
		{
			name:     "raw_text alias coalesces into extracted_text",
			req:      createReq{RelPath: "a.pdf", RawText: "parsed text"},
			wantText: "parsed text",
		},
		{
			name:     "extracted_text wins when both set",
			req:      createReq{RelPath: "a.pdf", ExtractedText: "primary", RawText: "alias"},
			wantText: "primary",
		},
		{
			name:    "oversize extracted_text rejected",
			req:     createReq{RelPath: "a.pdf", RawText: strings.Repeat("x", maxExtractedTextBytes+1)},
			wantErr: "extracted_text_too_large",
		},
		{
			name:    "bad hex content_hash rejected",
			req:     createReq{RelPath: "a.pdf", ContentHashHex: "not-hex!!"},
			wantErr: "bad_content_hash",
		},
		{
			name:        "valid hex content_hash decoded",
			req:         createReq{RelPath: "a.pdf", ContentHashHex: hashHex},
			wantHashLen: 32,
		},
		{
			name:         "parse_meta whitelist keeps declared keys and drops extras",
			req:          createReq{RelPath: "a.pdf", ParseMeta: map[string]any{"parser": "docproc-web", "version": "0.1.0", "format": "pdf", "page_count": float64(3), "evil": "dropped"}},
			wantMetaKeys: []string{"parser", "version", "format", "page_count"},
		},
		{
			name:         "nil parse_meta yields empty map",
			req:          createReq{RelPath: "a.pdf"},
			wantMetaKeys: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hash, meta, errCode := normalizeCreateReq(&tc.req)
			if errCode != tc.wantErr {
				t.Fatalf("errCode = %q, want %q", errCode, tc.wantErr)
			}
			if tc.wantErr != "" {
				return
			}
			if tc.wantText != "" && tc.req.ExtractedText != tc.wantText {
				t.Errorf("ExtractedText = %q, want %q", tc.req.ExtractedText, tc.wantText)
			}
			if len(hash) != tc.wantHashLen {
				t.Errorf("contentHash len = %d, want %d", len(hash), tc.wantHashLen)
			}
			if meta == nil {
				t.Fatal("parseMeta should never be nil")
			}
			if len(meta) != len(tc.wantMetaKeys) {
				t.Errorf("parseMeta keys = %v, want %v", meta, tc.wantMetaKeys)
			}
			for _, k := range tc.wantMetaKeys {
				if _, ok := meta[k]; !ok {
					t.Errorf("parseMeta missing key %q", k)
				}
			}
		})
	}
}
