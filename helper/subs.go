package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// cleanSubName is the filtered ASS the job reads as input 1 in place of the
// original track. It lives in the output dir so cleanOutDir sweeps it.
const cleanSubName = "subs.ass"

var (
	assDrawing  = regexp.MustCompile(`\\p[1-9]`)
	assOverride = regexp.MustCompile(`\{[^}]*\}`)
	// {=N} is an Aegisub reference ffmpeg's ASS decoder leaves in as text.
	assExtradata = regexp.MustCompile(`\{=\d+\}`)
)

// minCueSeconds drops motion-tracked typesetting: signs drawn one event per
// frame (≈0.04s) per layer. A fansub sign scene can be thousands of these in
// a few seconds; ffmpeg turns each into a WebVTT cue, falls behind, and the
// subtitle segments end up ~20s behind the video, which froze the receiver.
// 0.1s is above two frames: at 0.06s two-frame tracking survives (147 cues
// in 10s in one episode); at 0.1s no episode measured passed 23. No dialogue
// line in those scripts was shorter than 0.25s, and nothing under 0.1s is
// readable anyway.
const minCueSeconds = 0.1

// CleanASS strips what WebVTT can't show from an ASS script — vector
// drawings, frame-by-frame typesetting, and the duplicate layers a styled
// line is built from (border, shadow, fill) — keeping dialogue and static
// signs. Non-Dialogue lines pass through so ffmpeg still parses the script.
// ponytail: assumes the standard [Events] Format field order (every
// Aegisub-written script uses it); parse the Format line if one turns up
// that doesn't.
func CleanASS(script string) string {
	var out strings.Builder
	seen := map[string]bool{}
	for _, line := range strings.SplitAfter(script, "\n") {
		rest, ok := strings.CutPrefix(line, "Dialogue:")
		if !ok {
			out.WriteString(line)
			continue
		}
		f := strings.SplitN(rest, ",", 10)
		if len(f) < 10 {
			continue
		}
		text := assExtradata.ReplaceAllString(f[9], "")
		plain := strings.TrimSpace(assOverride.ReplaceAllString(text, ""))
		start, end := assTime(f[1]), assTime(f[2])
		key := f[1] + "|" + f[2] + "|" + plain
		if plain == "" || assDrawing.MatchString(text) || end-start < minCueSeconds || seen[key] {
			continue
		}
		seen[key] = true
		f[9] = text
		out.WriteString("Dialogue:" + strings.Join(f, ","))
	}
	return out.String()
}

// assTime parses H:MM:SS.cc; a malformed time reads as 0, which makes the
// cue's duration nonsense and gets it dropped rather than crash the cast.
func assTime(s string) float64 {
	p := strings.Split(strings.TrimSpace(s), ":")
	if len(p) != 3 {
		return 0
	}
	h, _ := strconv.ParseFloat(p[0], 64)
	m, _ := strconv.ParseFloat(p[1], 64)
	sec, _ := strconv.ParseFloat(p[2], 64)
	return h*3600 + m*60 + sec
}

// PrepareASS points an ASS/SSA subtitle job at a cleaned copy of the track:
// read from the external file, or extracted from the source with ffmpeg.
// Any other codec, or any failure, leaves c untouched — the cast goes ahead
// with the unfiltered track rather than without subtitles.
func PrepareASS(c JobConfig) (JobConfig, error) {
	if !c.HasSub() || (c.SubCodec != "ass" && c.SubCodec != "ssa") {
		return c, nil
	}
	var raw []byte
	var err error
	if c.SubPath != "" {
		raw, err = os.ReadFile(c.SubPath)
	} else {
		raw, err = exec.Command(c.FFmpeg, "-v", "error", "-i", c.Source,
			"-map", "0:"+c.SubMap, "-c:s", "copy", "-f", "ass", "pipe:1").Output()
	}
	if err != nil {
		return c, fmt.Errorf("reading subtitle track: %w", err)
	}
	path := filepath.Join(c.OutDir, cleanSubName)
	if err := os.WriteFile(path, []byte(CleanASS(string(raw))), 0o644); err != nil {
		return c, err
	}
	c.SubPath, c.SubMap = path, ""
	return c, nil
}
