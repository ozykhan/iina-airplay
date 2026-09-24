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

// What ffmpeg's hlsenc writes to master.m3u8 while a stream-copy job is still
// running: the bitrate of a copied stream is unknown until the trailer, so the
// variant line is left out until packaging ends (issue #26).
const ffmpegMasterNoVariant = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="subtitle_0",DEFAULT=NO,AUTOSELECT=YES,URI="index_vtt.m3u8"

`

func TestEnsureVariantAddsMissingStreamInf(t *testing.T) {
	out := EnsureVariant(ffmpegMasterNoVariant, 5000000)
	want := "#EXT-X-STREAM-INF:BANDWIDTH=5000000,SUBTITLES=\"subs\"\nindex.m3u8\n"
	if !strings.HasSuffix(out, want) {
		t.Fatalf("variant not appended:\n%s", out)
	}
	if !strings.HasPrefix(out, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-MEDIA:") {
		t.Fatalf("header lines disturbed:\n%s", out)
	}
}

func TestEnsureVariantLeavesFfmpegVariantAlone(t *testing.T) {
	if got := EnsureVariant(ffmpegMaster, 5000000); got != ffmpegMaster {
		t.Fatalf("a master that already has a variant must pass through:\n%s", got)
	}
}

func TestEnsureVariantWithoutSubtitleGroup(t *testing.T) {
	out := EnsureVariant("#EXTM3U\n#EXT-X-VERSION:7\n", 5000000)
	if !strings.HasSuffix(out, "#EXT-X-STREAM-INF:BANDWIDTH=5000000\nindex.m3u8\n") {
		t.Fatalf("no SUBTITLES attr expected without a subtitle group:\n%s", out)
	}
}

func TestEstimateBandwidth(t *testing.T) {
	// 1 GB over 1000 s is 8 Mbit/s; 10% headroom on top.
	if got := EstimateBandwidth(1_000_000_000, 1000); got != 8_800_000 {
		t.Fatalf("got %d, want 8800000", got)
	}
	for _, c := range []struct {
		size int64
		dur  float64
	}{{0, 1000}, {1_000_000_000, 0}, {-1, -1}} {
		if got := EstimateBandwidth(c.size, c.dur); got != fallbackBandwidth {
			t.Fatalf("EstimateBandwidth(%d, %v) = %d, want fallback %d", c.size, c.dur, got, fallbackBandwidth)
		}
	}
}
