package transcribe

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/transcribe/types"
)

func TestMediaFormat(t *testing.T) {
	cases := []struct {
		kind, mime string
		want       types.MediaFormat
		wantErr    bool
	}{
		{"voice", "audio/ogg", types.MediaFormatOgg, false},
		{"video_note", "", types.MediaFormatMp4, false},
		{"audio", "audio/mpeg", types.MediaFormatMp3, false},
		{"audio", "audio/x-m4a", types.MediaFormatM4a, false},
		{"video", "video/mp4", types.MediaFormatMp4, false},
		{"audio", "application/zip", "", true},
	}
	for _, c := range cases {
		got, err := MediaFormat(c.kind, c.mime)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("MediaFormat(%q,%q) = %q,%v", c.kind, c.mime, got, err)
		}
	}
}

func TestSanitize(t *testing.T) {
	if got := sanitize("AgAD+/x_y-z.1"); got != "AgADx_y-z.1" {
		t.Errorf("sanitize = %q", got)
	}
}
