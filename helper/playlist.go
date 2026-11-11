package main

import (
	"strconv"
	"strings"
)

// fallbackBandwidth is the BANDWIDTH advertised when the source's size or
// duration is unknown. With a single variant nothing is chosen by it; the
// attribute just has to be present and plausible.
const fallbackBandwidth = 20_000_000

// EstimateBandwidth derives a BANDWIDTH value (bits/s) from the source file's
// size and duration, with 10% headroom — the same margin ffmpeg adds.
func EstimateBandwidth(sizeBytes int64, duration float64) int {
	if sizeBytes <= 0 || duration <= 0 {
		return fallbackBandwidth
	}
	return int(float64(sizeBytes) * 8 / duration * 1.1)
}

// AdvertisedBandwidth is the BANDWIDTH to advertise for this job. A remux
// streams at the source's own rate. A re-encode streams at the encoder's
// target instead, which for an efficient codec like AV1 can be several times
// the source's rate; advertising the smaller figure understates the stream.
func AdvertisedBandwidth(c JobConfig, srcSize int64) int {
	bw := EstimateBandwidth(srcSize, c.Duration)
	if c.Transcodes() {
		// Video target plus the largest audio encode (640k E-AC-3), with
		// the same 10% headroom EstimateBandwidth uses.
		if t := int(float64(c.TranscodeBitrate()+640_000) * 1.1); t > bw {
			return t
		}
	}
	return bw
}

// EnsureVariant adds the #EXT-X-STREAM-INF line an ffmpeg master playlist
// lacks while a stream-copy job is still running. hlsenc only learns a copied
// stream's bitrate at the trailer, so until packaging ends its master names
// the subtitle rendition and no variant at all — and AVFoundation rejects
// that playlist (media error 3 in WebKit; issue #26). A master that already
// has a variant passes through untouched.
func EnsureVariant(content string, bandwidth int) string {
	if strings.Contains(content, "#EXT-X-STREAM-INF:") {
		return content
	}
	inf := "#EXT-X-STREAM-INF:BANDWIDTH=" + strconv.Itoa(bandwidth)
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "#EXT-X-MEDIA:") || !strings.Contains(line, "TYPE=SUBTITLES") {
			continue
		}
		for _, a := range splitAttrs(strings.TrimPrefix(line, "#EXT-X-MEDIA:")) {
			if group, ok := strings.CutPrefix(a, "GROUP-ID="); ok {
				inf += ",SUBTITLES=" + group
			}
		}
		break
	}
	return strings.TrimRight(content, "\n") + "\n" + inf + "\nindex.m3u8\n"
}

// AddVariantAttrs adds CODECS, VIDEO-RANGE and FRAME-RATE to the master's
// variant line (ffmpeg's, or the one EnsureVariant synthesized) when they are
// missing; an empty field adds nothing. Existing attributes are preserved,
// except an AAC-LC CODECS entry is corrected to HE-AAC/HE-AAC v2 when
// VariantAttrs detects backward-compatible SBR/PS signaling in init.mp4.
// ffmpeg can miss that signaling in its end-of-job master playlist.
func AddVariantAttrs(content string, v VariantInfo) string {
	if v == (VariantInfo{}) {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, "#EXT-X-STREAM-INF:")
		if !ok {
			continue
		}
		attrs := splitAttrs(rest)
		if v.Codecs != "" {
			attrs = addCodecsAttr(attrs, v.Codecs)
		}
		if v.VideoRange != "" {
			attrs = addAttr(attrs, "VIDEO-RANGE", v.VideoRange)
		}
		if v.FrameRate != "" {
			attrs = addAttr(attrs, "FRAME-RATE", v.FrameRate)
		}
		lines[i] = "#EXT-X-STREAM-INF:" + strings.Join(attrs, ",")
	}
	return strings.Join(lines, "\n")
}

// addCodecsAttr trusts ffmpeg's codec list except for the AAC-LC core of
// backward-compatible HE-AAC. Correct only that exact entry, preserving the
// video codec, other entries, and their order.
func addCodecsAttr(attrs []string, derived string) []string {
	var aac string
	for _, codec := range strings.Split(derived, ",") {
		if codec == "mp4a.40.5" || codec == "mp4a.40.29" {
			aac = codec
			break
		}
	}
	if aac != "" {
		for i, attr := range attrs {
			value, ok := strings.CutPrefix(attr, `CODECS="`)
			if !ok || !strings.HasSuffix(value, `"`) {
				continue
			}
			codecs := strings.Split(strings.TrimSuffix(value, `"`), ",")
			for j, codec := range codecs {
				if codec == "mp4a.40.2" {
					codecs[j] = aac
				}
			}
			attrs[i] = "CODECS=" + quoteAttr(strings.Join(codecs, ","))
			return attrs
		}
	}
	return addAttr(attrs, "CODECS", quoteAttr(derived))
}

// RewriteMasterPlaylist fixes up the subtitle rendition line of an
// ffmpeg-written HLS master playlist. Older ffmpeg hardcodes DEFAULT=NO on
// the subtitle rendition; every version emits a generic NAME and no
// LANGUAGE, which leaves subtitles unlabeled (and, on older ffmpeg, off
// until the viewer digs through the TV's menu); the caller knows the
// track's real name and language, so the server rewrites the line at serve
// time either way.
func RewriteMasterPlaylist(content, name, lang string) string {
	if name == "" {
		name = "Subtitles"
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "#EXT-X-MEDIA:") || !strings.Contains(line, "TYPE=SUBTITLES") {
			continue
		}
		attrs := splitAttrs(strings.TrimPrefix(line, "#EXT-X-MEDIA:"))
		attrs = setAttr(attrs, "DEFAULT", "YES")
		attrs = setAttr(attrs, "AUTOSELECT", "YES")
		attrs = setAttr(attrs, "NAME", quoteAttr(name))
		if lang != "" {
			attrs = setAttr(attrs, "LANGUAGE", quoteAttr(lang))
		}
		lines[i] = "#EXT-X-MEDIA:" + strings.Join(attrs, ",")
	}
	return strings.Join(lines, "\n")
}

// splitAttrs splits an m3u8 attribute list on commas outside double quotes.
func splitAttrs(s string) []string {
	var out []string
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

func setAttr(attrs []string, key, val string) []string {
	for i, a := range attrs {
		if strings.HasPrefix(a, key+"=") {
			attrs[i] = key + "=" + val
			return attrs
		}
	}
	return append(attrs, key+"="+val)
}

// addAttr appends key=val unless the list already has key; unlike setAttr
// it never overwrites.
func addAttr(attrs []string, key, val string) []string {
	for _, a := range attrs {
		if strings.HasPrefix(a, key+"=") {
			return attrs
		}
	}
	return append(attrs, key+"="+val)
}

// quoteAttr renders a quoted-string attribute value; interior double quotes
// are illegal in the format, so they are stripped rather than escaped.
func quoteAttr(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, "") + `"`
}
