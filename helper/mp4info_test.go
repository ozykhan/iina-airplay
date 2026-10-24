package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// ---- minimal MP4 box builders: just enough structure for ParseInitSegment ----

func box(typ string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	b := binary.BigEndian.AppendUint32(nil, uint32(n))
	b = append(b, typ...)
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

func fullBox(typ string, payload ...[]byte) []byte {
	return box(typ, append([][]byte{{0, 0, 0, 0}}, payload...)...)
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// visualEntry is a VisualSampleEntry: 8 bytes of SampleEntry, 70 of visual
// fields, then child boxes (hvcC/avcC, colr, pasp...).
func visualEntry(fourcc string, children ...[]byte) []byte {
	head := make([]byte, 78)
	head[7] = 1 // data_reference_index
	return box(fourcc, append([][]byte{head}, children...)...)
}

// audioEntry is an AudioSampleEntry; version 1 and 2 (QuickTime sound
// descriptions) carry 16 and 36 extra bytes before the child boxes.
func audioEntry(fourcc string, version int, children ...[]byte) []byte {
	head := make([]byte, 28)
	head[7] = 1
	head[9] = byte(version)
	head[17] = 2 // channelcount
	switch version {
	case 1:
		head = append(head, make([]byte, 16)...)
	case 2:
		head = append(head, make([]byte, 36)...)
	}
	return box(fourcc, append([][]byte{head}, children...)...)
}

// trak wraps one sample entry; handler is the hdlr type ("vide", "soun"),
// which is what tells a parser how the entry is laid out.
func trak(handler string, entry []byte) []byte {
	hdlr := fullBox("hdlr", u32(0), []byte(handler), make([]byte, 13))
	return box("trak", box("mdia", hdlr, box("minf", box("stbl", fullBox("stsd", u32(1), entry)))))
}

func initSegment(traks ...[]byte) []byte {
	ftyp := box("ftyp", []byte("iso5\x00\x00\x02\x00iso5iso6mp41"))
	return append(ftyp, box("moov", append([][]byte{fullBox("mvhd", make([]byte, 96))}, traks...)...)...)
}

// hvcC: configurationVersion, then profile_space(2)|tier(1)|profile_idc(5),
// 32 compatibility flags, 48 constraint bits, level_idc, and the rest of the
// record (zeroed; the codec string doesn't read it).
func hvcC(space, tier, profile byte, compat uint32, constraint [6]byte, level byte) []byte {
	p := []byte{1, space<<6 | tier<<5 | profile}
	p = binary.BigEndian.AppendUint32(p, compat)
	p = append(p, constraint[:]...)
	p = append(p, level)
	p = append(p, make([]byte, 10)...)
	return box("hvcC", p)
}

func colr(kind string, prim, trc, matrix uint16) []byte {
	p := []byte(kind)
	p = binary.BigEndian.AppendUint16(p, prim)
	p = binary.BigEndian.AppendUint16(p, trc)
	p = binary.BigEndian.AppendUint16(p, matrix)
	if kind == "nclx" {
		p = append(p, 0) // full_range_flag + reserved
	}
	return box("colr", p)
}

// desc encodes an MPEG-4 descriptor with the 4-byte expandable size ffmpeg
// writes (0x80 0x80 0x80 n), which a naive one-byte size read gets wrong.
func desc(tag byte, payload ...[]byte) []byte {
	n := 0
	for _, p := range payload {
		n += len(p)
	}
	b := []byte{tag, 0x80, 0x80, 0x80, byte(n)}
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

func esds(oti byte, asc []byte) []byte {
	dcd := append([]byte{oti, 0x15, 0, 0, 0}, make([]byte, 8)...)
	return fullBox("esds", desc(0x03, []byte{0, 2, 0}, desc(0x04, dcd, desc(0x05, asc)), desc(0x06, []byte{2})))
}

var (
	constraintB0 = [6]byte{0xB0}
	ascAACLC     = []byte{0x12, 0x10} // AOT 2, 44.1 kHz, stereo
)

func TestParseInitSegmentHDR10HEVCWithEAC3(t *testing.T) {
	seg := initSegment(
		trak("vide", visualEntry("hvc1", hvcC(0, 0, 2, 0x20000000, [6]byte{0x90}, 93), colr("nclx", 9, 16, 9))),
		trak("soun", audioEntry("ec-3", 0, box("dec3", []byte{0, 0x60, 0x02, 0, 0}))),
	)
	got, err := ParseInitSegment(seg)
	if err != nil {
		t.Fatal(err)
	}
	want := InitInfo{Codecs: []string{"hvc1.2.4.L93.90", "ec-3"}, HasColr: true, Transfer: 16}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestHEVCCodecStringFields(t *testing.T) {
	for _, c := range []struct {
		name        string
		fourcc      string
		space, tier byte
		profile     byte
		compat      uint32
		constraint  [6]byte
		level       byte
		want        string
	}{
		// Apple's HLS authoring examples: Main at 3.1 and Main 10 at 5.0.
		{"main", "hvc1", 0, 0, 1, 0x60000000, constraintB0, 93, "hvc1.1.6.L93.B0"},
		{"main10", "hvc1", 0, 0, 2, 0x20000000, constraintB0, 150, "hvc1.2.4.L150.B0"},
		{"high tier, hev1", "hev1", 0, 1, 2, 0x20000000, constraintB0, 153, "hev1.2.4.H153.B0"},
		{"profile space", "hvc1", 1, 0, 4, 0x08000000, constraintB0, 120, "hvc1.A4.10.L120.B0"},
		{"interior zero bytes kept", "hvc1", 0, 0, 2, 0x20000000, [6]byte{0xB0, 0, 0, 0x01}, 120, "hvc1.2.4.L120.B0.0.0.1"},
		{"no constraint bytes", "hvc1", 0, 0, 2, 0x20000000, [6]byte{}, 120, "hvc1.2.4.L120"},
	} {
		seg := initSegment(trak("vide", visualEntry(c.fourcc, hvcC(c.space, c.tier, c.profile, c.compat, c.constraint, c.level))))
		got, err := ParseInitSegment(seg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got.Codecs) != 1 || got.Codecs[0] != c.want {
			t.Fatalf("%s: codecs %v, want [%s]", c.name, got.Codecs, c.want)
		}
	}
}

func TestParseInitSegmentH264WithAAC(t *testing.T) {
	seg := initSegment(
		trak("vide", visualEntry("avc1", box("avcC", []byte{1, 0x42, 0xC0, 0x1E, 0xFF, 0xE1}))),
		trak("soun", audioEntry("mp4a", 0, esds(0x40, ascAACLC))),
	)
	got, err := ParseInitSegment(seg)
	if err != nil {
		t.Fatal(err)
	}
	// ffmpeg's own hlsenc writes exactly this pair for an H.264 + AAC-LC stream.
	if want := []string{"avc1.42c01e", "mp4a.40.2"}; !reflect.DeepEqual(got.Codecs, want) {
		t.Fatalf("codecs %v, want %v", got.Codecs, want)
	}
	if got.HasColr {
		t.Fatalf("no colr box, but HasColr set: %+v", got)
	}
}

func TestAACObjectTypes(t *testing.T) {
	for _, c := range []struct {
		name string
		asc  []byte
		want string
	}{
		{"HE-AAC", []byte{0x2A, 0x10}, "mp4a.40.5"},
		// AOT 31 escapes to 32 + the next 6 bits: 42 (USAC) is 11111 001010.
		{"escaped AOT", []byte{0xF9, 0x40}, "mp4a.40.42"},
	} {
		got, err := ParseInitSegment(initSegment(trak("soun", audioEntry("mp4a", 0, esds(0x40, c.asc)))))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got.Codecs) != 1 || got.Codecs[0] != c.want {
			t.Fatalf("%s: codecs %v, want [%s]", c.name, got.Codecs, c.want)
		}
	}
}

func TestAudioSampleEntryVersions(t *testing.T) {
	for _, v := range []int{1, 2} {
		got, err := ParseInitSegment(initSegment(trak("soun", audioEntry("mp4a", v, esds(0x40, ascAACLC)))))
		if err != nil {
			t.Fatalf("v%d: %v", v, err)
		}
		if len(got.Codecs) != 1 || got.Codecs[0] != "mp4a.40.2" {
			t.Fatalf("v%d: codecs %v", v, got.Codecs)
		}
	}
	got, err := ParseInitSegment(initSegment(trak("soun", audioEntry("ac-3", 0, box("dac3", []byte{0x10, 0x3D, 0xC0})))))
	if err != nil || len(got.Codecs) != 1 || got.Codecs[0] != "ac-3" {
		t.Fatalf("ac-3: %v %v", got.Codecs, err)
	}
}

// A CODECS attribute must name every codec in the variant, so one track the
// parser can't name drops the whole list; the colour info still comes through.
func TestUnknownTrackDropsAllCodecs(t *testing.T) {
	for _, c := range []struct {
		name string
		seg  []byte
	}{
		{"unknown video entry", initSegment(
			trak("vide", visualEntry("av01", box("av1C", []byte{0x81, 0, 0, 0}), colr("nclx", 9, 18, 9))),
			trak("soun", audioEntry("mp4a", 0, esds(0x40, ascAACLC))))},
		{"mp3 in mp4a", initSegment(
			trak("vide", visualEntry("hvc1", hvcC(0, 0, 2, 0x20000000, constraintB0, 120), colr("nclx", 9, 18, 9))),
			trak("soun", audioEntry("mp4a", 0, esds(0x6B, nil))))},
		{"hvc1 without hvcC", initSegment(
			trak("vide", visualEntry("hvc1", colr("nclx", 9, 18, 9))))},
	} {
		got, err := ParseInitSegment(c.seg)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.Codecs != nil {
			t.Fatalf("%s: codecs %v, want none", c.name, got.Codecs)
		}
		if !got.HasColr || got.Transfer != 18 {
			t.Fatalf("%s: colour lost: %+v", c.name, got)
		}
	}
}

func TestQuickTimeNclcColr(t *testing.T) {
	got, err := ParseInitSegment(initSegment(trak("vide", visualEntry("hvc1",
		hvcC(0, 0, 2, 0x20000000, constraintB0, 120), colr("nclc", 9, 16, 9)))))
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasColr || got.Transfer != 16 {
		t.Fatalf("nclc colr not read: %+v", got)
	}
}

func TestICCColrIsNotColourInfo(t *testing.T) {
	got, err := ParseInitSegment(initSegment(trak("vide", visualEntry("hvc1",
		hvcC(0, 0, 2, 0x20000000, constraintB0, 120), box("colr", []byte("prof\x00\x00\x00\x00"))))))
	if err != nil {
		t.Fatal(err)
	}
	if got.HasColr {
		t.Fatalf("an ICC colr carries no transfer function: %+v", got)
	}
}

// ffmpeg may still be writing init.mp4 when the TV asks for the master. A
// short read must fail, never yield a partial CODECS list.
func TestTruncatedInitSegmentFails(t *testing.T) {
	seg := initSegment(
		trak("vide", visualEntry("hvc1", hvcC(0, 0, 2, 0x20000000, constraintB0, 120))),
		trak("soun", audioEntry("ec-3", 0)),
	)
	for _, n := range []int{0, 10, len(seg) / 2, len(seg) - 1} {
		if got, err := ParseInitSegment(seg[:n]); err == nil {
			t.Fatalf("truncated to %d bytes: got %+v, want an error", n, got)
		}
	}
}

func TestVideoRange(t *testing.T) {
	for _, c := range []struct {
		name string
		info InitInfo
		hint string
		want string
	}{
		{"colr PQ", InitInfo{HasColr: true, Transfer: 16}, "", "PQ"},
		{"colr HLG", InitInfo{HasColr: true, Transfer: 18}, "", "HLG"},
		{"colr SDR", InitInfo{HasColr: true, Transfer: 1}, "", ""},
		// colr is what the TV's demuxer reads; it outranks the plugin's hint.
		{"colr SDR beats hint", InitInfo{HasColr: true, Transfer: 1}, "PQ", ""},
		{"no colr, hint PQ", InitInfo{}, "PQ", "PQ"},
		{"no colr, hint HLG", InitInfo{}, "HLG", "HLG"},
		{"no colr, no hint", InitInfo{}, "", ""},
	} {
		if got := c.info.VideoRange(c.hint); got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestVariantAttrsFromFiles(t *testing.T) {
	dir := t.TempDir()
	// No init.mp4 yet: nothing from the files, the hint still applies.
	if got := VariantAttrs(dir, "HLG"); got != (VariantInfo{VideoRange: "HLG"}) {
		t.Fatalf("missing init.mp4: got %+v", got)
	}
	os.WriteFile(filepath.Join(dir, "init.mp4"), hdrInit(0, 24000, 0), 0o644)
	// No first segment yet: everything but the frame rate.
	if got := VariantAttrs(dir, ""); got != (VariantInfo{Codecs: "hvc1.2.4.L150.B0,ec-3", VideoRange: "PQ"}) {
		t.Fatalf("missing segment: got %+v", got)
	}
	os.WriteFile(filepath.Join(dir, "seg_0000.m4s"), mediaSegment(traf(tfhd(1, 1001), trunDurations(1001, 1001))), 0o644)
	want := VariantInfo{Codecs: "hvc1.2.4.L150.B0,ec-3", VideoRange: "PQ", FrameRate: "23.976"}
	if got := VariantAttrs(dir, ""); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// SDR variants played without FRAME-RATE before; they don't get one now.
func TestVariantAttrsFrameRateOnlyForHDR(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "init.mp4"), initSegment(
		timedTrak("vide", 0, 1, 24000, visualEntry("avc1", box("avcC", []byte{1, 0x64, 0, 0x28})))), 0o644)
	os.WriteFile(filepath.Join(dir, "seg_0000.m4s"), mediaSegment(traf(tfhd(1, 1001), trunDurations(1001, 1001))), 0o644)
	if got := VariantAttrs(dir, ""); got != (VariantInfo{Codecs: "avc1.640028"}) {
		t.Fatalf("got %+v", got)
	}
}

// ---- frame rate: init.mp4 timing fields + the first media segment ----

// tkhd/mdhd with the 32-bit (v0) or 64-bit (v1) time fields before the value
// the parser wants (track_ID, timescale).
func tkhd(version byte, id uint32) []byte {
	var p []byte
	if version == 1 {
		p = append([]byte{1, 0, 0, 3}, make([]byte, 16)...)
	} else {
		p = append([]byte{0, 0, 0, 3}, make([]byte, 8)...)
	}
	return box("tkhd", append(binary.BigEndian.AppendUint32(p, id), make([]byte, 60)...))
}

func mdhd(version byte, timescale uint32) []byte {
	var p []byte
	if version == 1 {
		p = append([]byte{1, 0, 0, 0}, make([]byte, 16)...)
	} else {
		p = append([]byte{0, 0, 0, 0}, make([]byte, 8)...)
	}
	return box("mdhd", append(binary.BigEndian.AppendUint32(p, timescale), make([]byte, 8)...))
}

func timedTrak(handler string, version byte, id, timescale uint32, entry []byte) []byte {
	hdlr := fullBox("hdlr", u32(0), []byte(handler), make([]byte, 13))
	return box("trak", tkhd(version, id), box("mdia", mdhd(version, timescale), hdlr,
		box("minf", box("stbl", fullBox("stsd", u32(1), entry)))))
}

func trex(id, defaultDuration uint32) []byte {
	return fullBox("trex", u32(id), u32(1), u32(defaultDuration), u32(0), u32(0))
}

// hdrInit is an HDR10 init segment shaped like ffmpeg's: video is track 1 at
// the given timescale, E-AC-3 is track 2, and mvex carries trex defaults.
func hdrInit(version byte, timescale, trexVideoDuration uint32) []byte {
	ftyp := box("ftyp", []byte("iso5\x00\x00\x02\x00iso5iso6mp41"))
	return append(ftyp, box("moov", fullBox("mvhd", make([]byte, 96)),
		timedTrak("vide", version, 1, timescale, visualEntry("hvc1", hvcC(0, 0, 2, 0x20000000, constraintB0, 150), colr("nclx", 9, 16, 9))),
		timedTrak("soun", version, 2, 48000, audioEntry("ec-3", 0)),
		box("mvex", trex(1, trexVideoDuration), trex(2, 1536)))...)
}

func tfhd(id uint32, defaultDuration uint32) []byte {
	flags := uint32(0x020000)
	p := []byte{}
	if defaultDuration != 0 {
		flags |= 0x08
		p = u32(defaultDuration)
	}
	return box("tfhd", append(append(u32(flags), u32(id)...), p...))
}

// trunDurations writes ffmpeg's trun shape (data offset, first-sample flags,
// then per-sample duration, size and composition offset).
func trunDurations(durations ...uint32) []byte {
	p := append(u32(0x000B05), u32(uint32(len(durations)))...)
	p = append(p, u32(0)...) // data_offset
	p = append(p, u32(0)...) // first_sample_flags
	for _, d := range durations {
		p = append(p, u32(d)...)
		p = append(p, u32(1000)...) // size
		p = append(p, u32(0)...)    // composition offset
	}
	return box("trun", p)
}

// trunNoDurations writes a trun whose samples take the default duration.
func trunNoDurations(count uint32) []byte {
	p := append(u32(0x000201), u32(count)...)
	p = append(p, u32(0)...)
	for i := uint32(0); i < count; i++ {
		p = append(p, u32(1000)...)
	}
	return box("trun", p)
}

func traf(tfhdBox []byte, truns ...[]byte) []byte {
	return box("traf", append([][]byte{tfhdBox, fullBox("tfdt", u32(0))}, truns...)...)
}

// mediaSegment is styp + sidx + moof + the head of an mdat: the bytes a
// bounded read of a real first segment returns.
func mediaSegment(trafs ...[]byte) []byte {
	seg := box("styp", []byte("msdh\x00\x00\x00\x00msdhmsix"))
	seg = append(seg, fullBox("sidx", make([]byte, 40))...)
	seg = append(seg, box("moof", append([][]byte{fullBox("mfhd", u32(1))}, trafs...)...)...)
	mdat := binary.BigEndian.AppendUint32(nil, 50_000_000) // declared far past what was read
	return append(append(seg, mdat...), "mdat\x00\x00\x00\x00"...)
}

func TestFrameRateFromPerSampleDurations(t *testing.T) {
	info, err := ParseInitSegment(hdrInit(0, 24000, 0))
	if err != nil {
		t.Fatal(err)
	}
	seg := mediaSegment(
		traf(tfhd(2, 1536), trunNoDurations(10)), // audio first: must be skipped
		traf(tfhd(1, 1001), trunDurations(1001, 1001, 1001, 1001)),
	)
	if got, err := FragmentFrameRate(seg, info); err != nil || got != "23.976" {
		t.Fatalf("got %q, %v; want 23.976", got, err)
	}
}

// A 24 fps source in ms timestamps lands in a 16000 timescale as alternating
// 666/667-tick durations: the rate is the average, never 16000/min.
func TestFrameRateAveragesUnevenDurations(t *testing.T) {
	info, _ := ParseInitSegment(hdrInit(0, 16000, 0))
	var d []uint32
	for i := 0; i < 24; i++ {
		d = append(d, 666+uint32(i%3/2)) // 666, 666, 667, ...
	}
	seg := mediaSegment(traf(tfhd(1, 0), trunDurations(d...)))
	got, err := FragmentFrameRate(seg, info)
	if err != nil {
		t.Fatal(err)
	}
	if got != "24.012" {
		t.Fatalf("got %q, want 24.012 (16000*24/15992)", got)
	}
}

func TestFrameRateFromTfhdDefault(t *testing.T) {
	info, _ := ParseInitSegment(hdrInit(1, 12800, 0)) // v1 tkhd/mdhd layouts too
	seg := mediaSegment(traf(tfhd(1, 512), trunNoDurations(50), trunNoDurations(25)))
	if got, err := FragmentFrameRate(seg, info); err != nil || got != "25.000" {
		t.Fatalf("got %q, %v; want 25.000", got, err)
	}
}

func TestFrameRateFromTrexDefault(t *testing.T) {
	info, _ := ParseInitSegment(hdrInit(0, 90000, 3750))
	seg := mediaSegment(traf(tfhd(1, 0), trunNoDurations(24)))
	if got, err := FragmentFrameRate(seg, info); err != nil || got != "24.000" {
		t.Fatalf("got %q, %v; want 24.000", got, err)
	}
}

func TestFrameRateUnavailable(t *testing.T) {
	info, _ := ParseInitSegment(hdrInit(0, 24000, 0))
	full := mediaSegment(traf(tfhd(1, 1001), trunDurations(1001, 1001)))
	moofEnd := len(full) - 16 // the mdat header is the last 16 bytes
	for _, c := range []struct {
		name string
		seg  []byte
		info InitInfo
	}{
		{"truncated moof", full[:moofEnd-5], info},
		{"no moof yet", full[:60], info},
		{"no video traf", mediaSegment(traf(tfhd(2, 1536), trunNoDurations(10))), info},
		{"no duration anywhere", mediaSegment(traf(tfhd(1, 0), trunNoDurations(10))), info},
		{"init without timing", full, InitInfo{}},
		// flags promise a data offset and first-sample flags the box lacks
		{"trun shorter than its flags", mediaSegment(traf(tfhd(1, 0), box("trun", u32(0x000B05), u32(5)))), info},
	} {
		if got, err := FragmentFrameRate(c.seg, c.info); err == nil {
			t.Fatalf("%s: got %q, want an error", c.name, got)
		}
	}
}

func TestInitSegmentReadsVideoTiming(t *testing.T) {
	for _, v := range []byte{0, 1} {
		info, err := ParseInitSegment(hdrInit(v, 16000, 640))
		if err != nil {
			t.Fatal(err)
		}
		if info.VideoTrackID != 1 || info.VideoTimescale != 16000 || info.VideoTrexDuration != 640 {
			t.Fatalf("v%d: timing not read: %+v", v, info)
		}
	}
}

// The parsers read files ffmpeg may be halfway through writing; no input may
// panic them. The seeds run with every go test; `go test -fuzz` explores.
func FuzzParseInitSegment(f *testing.F) {
	f.Add(hdrInit(0, 24000, 0))
	f.Add(hdrInit(1, 16000, 640))
	f.Add(initSegment(trak("soun", audioEntry("mp4a", 0, esds(0x40, ascAACLC)))))
	f.Fuzz(func(t *testing.T, b []byte) {
		ParseInitSegment(b)
	})
}

func FuzzFragmentFrameRate(f *testing.F) {
	info, _ := ParseInitSegment(hdrInit(0, 24000, 1001))
	f.Add(mediaSegment(traf(tfhd(1, 1001), trunDurations(1001, 1001))))
	f.Add(mediaSegment(traf(tfhd(1, 0), trunNoDurations(3))))
	f.Fuzz(func(t *testing.T, b []byte) {
		FragmentFrameRate(b, info)
	})
}
