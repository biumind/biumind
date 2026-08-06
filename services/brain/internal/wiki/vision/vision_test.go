package vision

import "testing"

func TestFindImages(t *testing.T) {
	text := "Hi ![](http://x.png) text ![alt text](http://y.jpg \"title\") and ![logo](http://z.svg)."
	got := FindImages(text)
	if len(got) != 3 {
		t.Fatalf("want 3 refs, got %d (%+v)", len(got), got)
	}
	if got[0].URL != "http://x.png" || got[0].Alt != "" {
		t.Errorf("ref 0 wrong: %+v", got[0])
	}
	if got[1].Alt != "alt text" || got[1].URL != "http://y.jpg" {
		t.Errorf("ref 1 wrong: %+v", got[1])
	}
	if got[2].Alt != "logo" {
		t.Errorf("ref 2 wrong: %+v", got[2])
	}
}

func TestNeedsCaption(t *testing.T) {
	cases := []struct {
		alt  string
		want bool
	}{
		{"", true},
		{"   ", true},
		{"img", true},
		{"IMG", true},
		{"figure", true},
		{"a real description", false},
		{"Q2 revenue chart", false},
		{"Logo", false}, // 4 chars, not in placeholder list
	}
	for _, c := range cases {
		if got := NeedsCaption(c.alt); got != c.want {
			t.Errorf("NeedsCaption(%q) = %v, want %v", c.alt, got, c.want)
		}
	}
}

func TestApplyCaptions_BasicSubstitution(t *testing.T) {
	text := "See ![](http://x.png) for details."
	got, changed := ApplyCaptions(text, map[string]string{
		"http://x.png": "Bar chart of Q2 revenue.",
	})
	want := "See ![Bar chart of Q2 revenue.](http://x.png) for details."
	if got != want || !changed {
		t.Errorf("got %q (changed=%v), want %q (changed=true)", got, changed, want)
	}
}

func TestApplyCaptions_PreservesExistingAlt(t *testing.T) {
	text := "Already ![Real caption](http://x.png) named."
	got, changed := ApplyCaptions(text, map[string]string{
		"http://x.png": "AI-generated caption",
	})
	if got != text || changed {
		t.Errorf("must not overwrite real alt; got %q changed=%v", got, changed)
	}
}

func TestApplyCaptions_FirstOccurrenceOnly(t *testing.T) {
	text := "![](http://x.png) and again ![](http://x.png)."
	got, _ := ApplyCaptions(text, map[string]string{"http://x.png": "C"})
	want := "![C](http://x.png) and again ![](http://x.png)."
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestApplyCaptions_MultipleURLs(t *testing.T) {
	text := "![](http://a.png) ![](http://b.png) ![named](http://c.png)"
	got, _ := ApplyCaptions(text, map[string]string{
		"http://a.png": "A",
		"http://b.png": "B",
	})
	want := "![A](http://a.png) ![B](http://b.png) ![named](http://c.png)"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestApplyCaptions_EscapesBadChars(t *testing.T) {
	got, _ := ApplyCaptions(
		"![](http://x.png)",
		map[string]string{"http://x.png": "Line one\nLine ]two   spaced"},
	)
	want := "![Line one Line two spaced](http://x.png)"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestApplyCaptions_NoOpWhenNoMatches(t *testing.T) {
	text := "no images here"
	got, changed := ApplyCaptions(text, map[string]string{"http://x.png": "C"})
	if got != text || changed {
		t.Errorf("expected no-op, got %q changed=%v", got, changed)
	}
}

func TestApplyCaptions_PlaceholderAltGetsReplaced(t *testing.T) {
	text := "![image](http://x.png) ![figure](http://y.png)"
	got, _ := ApplyCaptions(text, map[string]string{
		"http://x.png": "A",
		"http://y.png": "B",
	})
	want := "![A](http://x.png) ![B](http://y.png)"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestHashURL_Deterministic(t *testing.T) {
	a := HashURL("http://x.png")
	b := HashURL("http://x.png")
	c := HashURL("http://y.png")
	if string(a) != string(b) {
		t.Error("same URL → different hashes")
	}
	if string(a) == string(c) {
		t.Error("different URLs → same hash")
	}
}
