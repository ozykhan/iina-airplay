package main

import (
	"strings"
	"testing"
)

// Shape ffmpeg's hlsenc actually emits for a single variant + one subtitle
// rendition (attribute order matters to these tests only as "preserved").
const ffmpegMaster = `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="default",NAME="subtitle_0",DEFAULT=NO,AUTOSELECT=YES,URI="index_vtt.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1234567,CODECS="hvc1.2.4.L120.b0,mp4a.40.2",SUBTITLES="default"
index.m3u8
`

func TestRewriteSetsDefaultNameLanguage(t *testing.T) {
	out := RewriteMasterPlaylist(ffmpegMaster, "English (SDH)", "en")
	if !strings.Contains(out, "DEFAULT=YES") || strings.Contains(out, "DEFAULT=NO") {
		t.Fatalf("DEFAULT not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `NAME="English (SDH)"`) {
		t.Fatalf("NAME not rewritten:\n%s", out)
	}
	if !strings.Contains(out, `LANGUAGE="en"`) {
		t.Fatalf("LANGUAGE not added:\n%s", out)
	}
	if !strings.Contains(out, `URI="index_vtt.m3u8"`) {
		t.Fatalf("URI must be preserved:\n%s", out)
	}
}

func TestRewriteLeavesOtherLinesAlone(t *testing.T) {
	out := RewriteMasterPlaylist(ffmpegMaster, "English", "en")
	for _, line := range []string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		`#EXT-X-STREAM-INF:BANDWIDTH=1234567,CODECS="hvc1.2.4.L120.b0,mp4a.40.2",SUBTITLES="default"`,
		"index.m3u8",
	} {
		if !strings.Contains(out, line) {
			t.Fatalf("line %q disturbed:\n%s", line, out)
		}
	}
}

func TestRewriteFallbackNameAndNoLang(t *testing.T) {
	out := RewriteMasterPlaylist(ffmpegMaster, "", "")
	if !strings.Contains(out, `NAME="Subtitles"`) {
		t.Fatalf("empty name must fall back to Subtitles:\n%s", out)
	}
	if strings.Contains(out, "LANGUAGE=") {
		t.Fatalf("empty lang must not add LANGUAGE:\n%s", out)
	}
}

func TestRewriteRespectsQuotedCommasAndQuotesInName(t *testing.T) {
	in := `#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="a, b",NAME="x",DEFAULT=NO,URI="index_vtt.m3u8"`
	out := RewriteMasterPlaylist(in, `Eng "SDH", forced`, "en")
	if !strings.Contains(out, `GROUP-ID="a, b"`) {
		t.Fatalf("quoted comma split wrongly:\n%s", out)
	}
	// interior quotes are illegal in quoted-string attrs: stripped, comma kept
	if !strings.Contains(out, `NAME="Eng SDH, forced"`) {
		t.Fatalf("name not sanitized:\n%s", out)
	}
}

func TestRewriteIgnoresNonSubtitleMediaLines(t *testing.T) {
	in := `#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="a",DEFAULT=NO,URI="a.m3u8"`
	if got := RewriteMasterPlaylist(in, "English", "en"); got != in {
		t.Fatalf("audio media line must pass through untouched:\ngot:  %s\nwant: %s", got, in)
	}
}
