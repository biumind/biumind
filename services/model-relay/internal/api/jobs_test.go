// Unit tests for /v1/jobs handler — the pure logic pieces that don't
// need DB / NATS / billing. Integration tests covering full submit
// flow live in jobs_e2e_test.go (P4.S3.7).

package api

import (
	"testing"
)

func TestCeilMul(t *testing.T) {
	cases := []struct {
		base   int64
		factor float64
		want   int64
	}{
		{0, 1.0, 0},
		{10, 0, 0},
		{10, 1.0, 10},
		{10, 2.5, 25},
		{10, 2.51, 26}, // ceil
		{8, 90.0, 720}, // wanx-2.6-t2v 15s 1080p case from §3.1
		{40, 5.2, 208}, // ceil(40 * 5.2) = 208 (Q11 case)
	}
	for _, c := range cases {
		got := ceilMul(c.base, c.factor)
		if got != c.want {
			t.Errorf("ceilMul(%d, %g) = %d, want %d", c.base, c.factor, got, c.want)
		}
	}
}

func TestIsJobsModeSupported(t *testing.T) {
	yes := []string{
		"image_generation", "video_generation", "digital_human",
		"audio_speech", "audio_transcription", "hotparse",
	}
	no := []string{"chat", "embedding", "completion", "responses", "rerank", ""}

	for _, m := range yes {
		if !isJobsModeSupported(m) {
			t.Errorf("expected %q supported", m)
		}
	}
	for _, m := range no {
		if isJobsModeSupported(m) {
			t.Errorf("expected %q NOT supported (use other endpoint)", m)
		}
	}
}

func TestJobsTypeFromMode(t *testing.T) {
	cases := []struct{ mode, want string }{
		{"image_generation", "image"},
		{"video_generation", "video"},
		{"digital_human", "digital_human"},
		{"hotparse", "hotparse"},
		// audio falls back to "image" until aigc.tasks.type CHECK
		// constraint is widened in 段 3.4
		{"audio_speech", "image"},
		{"audio_transcription", "image"},
	}
	for _, c := range cases {
		got := jobsTypeFromMode(c.mode)
		if got != c.want {
			t.Errorf("jobsTypeFromMode(%q) = %q, want %q", c.mode, got, c.want)
		}
	}
}
