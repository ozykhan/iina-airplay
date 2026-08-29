package main

import "strings"

// RewriteMasterPlaylist fixes up the subtitle rendition line of an
// ffmpeg-written HLS master playlist. ffmpeg hardcodes DEFAULT=NO and a
// generic NAME, which leaves subtitles off until the viewer digs through the
// TV's menu; the caller knows the track's real name and language, so the
// server rewrites the line at serve time.
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

// quoteAttr renders a quoted-string attribute value; interior double quotes
// are illegal in the format, so they are stripped rather than escaped.
func quoteAttr(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, "") + `"`
}
