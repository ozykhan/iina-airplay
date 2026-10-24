package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// InitInfo is what the HLS master playlist needs to know about the tracks in
// the fMP4 init segment ffmpeg writes (init.mp4), and that ffmpeg's hlsenc
// leaves out of the variant line itself: it writes CODECS only for H.264 and
// never VIDEO-RANGE. Without VIDEO-RANGE a variant is SDR by definition, so
// an HDR stream is announced as SDR and the Apple TV rejects it (issue #45).
type InitInfo struct {
	// Codecs holds one RFC 6381 codec string per track, in track order. It
	// is nil when any track's codec can't be named: CODECS must list every
	// codec in the variant, and a partial list is worse than none.
	Codecs []string
	// HasColr is set when the video sample entry carries an nclx/nclc colr
	// box; Transfer is its transfer_characteristics (ITU-T H.273: 16 is PQ,
	// 18 is HLG). ffmpeg writes colr only when the source tags the transfer.
	HasColr  bool
	Transfer int
	// The video track's timing, for FragmentFrameRate: its tkhd track_ID,
	// mdhd timescale and trex default_sample_duration (0 when absent).
	VideoTrackID      uint32
	VideoTimescale    uint32
	VideoTrexDuration uint32
}

// VariantInfo holds the attributes the server adds to the master's variant
// line; an empty field adds nothing.
type VariantInfo struct {
	Codecs     string
	VideoRange string
	FrameRate  string
}

// VideoRange is the VIDEO-RANGE value for the variant, or "" for SDR (the
// attribute's default, left implicit so SDR casts keep the master they had).
// The colr box is what the TV's demuxer reads, so it outranks hint, which is
// the plugin's reading of the decoded stream (mpv's video-params/gamma) for
// sources that tag the transfer only in the bitstream.
func (i InitInfo) VideoRange(hint string) string {
	if i.HasColr {
		switch i.Transfer {
		case 16:
			return "PQ"
		case 18:
			return "HLG"
		}
		return ""
	}
	if hint == "PQ" || hint == "HLG" {
		return hint
	}
	return ""
}

// firstSegment is the media segment FRAME-RATE is measured on; BuildArgs
// names segments seg_%04d.m4s, and hlsenc writes this one, init.mp4 included,
// before the first playlist that lists it.
const firstSegment = "seg_0000.m4s"

// segmentPrefix bounds the read of the first segment: its moof sits at the
// start and is a few KB even for UHD, while the mdat after it can be tens
// of MB.
const segmentPrefix = 1 << 20

// VariantAttrs derives the variant attributes from the init segment and the
// first media segment in dir. Both are read fresh on every call: ffmpeg may
// still be writing them, and a read that fails now must not stick.
//
// FRAME-RATE is only added to an HDR variant. AVFoundation rejects an HDR
// variant without it ("HDR alternate is missing FRAME-RATE", -12642), while
// SDR masters have always played without one and are left as they were.
func VariantAttrs(dir, hint string) VariantInfo {
	var info InitInfo
	if b, err := os.ReadFile(filepath.Join(dir, "init.mp4")); err == nil {
		if parsed, perr := ParseInitSegment(b); perr == nil {
			info = parsed
		}
	}
	v := VariantInfo{Codecs: strings.Join(info.Codecs, ","), VideoRange: info.VideoRange(hint)}
	if v.VideoRange != "" {
		if seg, err := readPrefix(filepath.Join(dir, firstSegment), segmentPrefix); err == nil {
			v.FrameRate, _ = FragmentFrameRate(seg, info)
		}
	}
	return v
}

func readPrefix(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	got, err := io.ReadFull(f, b)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil
	}
	return b[:got], err
}

// FragmentFrameRate measures the video frame rate of a media segment's first
// fragment as FRAME-RATE wants it (three decimals): the timescale times the
// sample count over the summed sample durations. It is an average on
// purpose. A 24 fps source with millisecond timestamps lands in a 16000
// timescale as alternating 666/667-tick frames, so timescale/min would
// overstate the rate. seg may stop anywhere after the moof; a moof that is
// cut short is an error, never a rate from part of it.
func FragmentFrameRate(seg []byte, info InitInfo) (string, error) {
	if info.VideoTrackID == 0 || info.VideoTimescale == 0 {
		return "", errors.New("mp4: no video timing in init segment")
	}
	moof, err := firstMoof(seg)
	if err != nil {
		return "", err
	}
	boxes, err := readBoxes(moof)
	if err != nil {
		return "", err
	}
	var samples, ticks uint64
	for _, bx := range boxes {
		if bx.typ != "traf" {
			continue
		}
		n, d, ok, err := trafTiming(bx.body, info)
		if err != nil {
			return "", err
		}
		if ok {
			samples += n
			ticks += d
		}
	}
	if samples == 0 || ticks == 0 {
		return "", errors.New("mp4: no video samples in first fragment")
	}
	fps := float64(info.VideoTimescale) * float64(samples) / float64(ticks)
	if fps < 1 || fps > 1000 {
		return "", fmt.Errorf("mp4: implausible frame rate %.3f", fps)
	}
	return strconv.FormatFloat(fps, 'f', 3, 64), nil
}

// firstMoof walks top-level boxes (styp, sidx...) up to the first moof and
// returns its body. Unlike readBoxes it tolerates whatever follows the moof
// being cut off, which is the point of reading only a prefix.
func firstMoof(b []byte) ([]byte, error) {
	for len(b) >= 8 {
		size, hdr := uint64(binary.BigEndian.Uint32(b)), uint64(8)
		if size == 1 {
			if len(b) < 16 {
				return nil, errTruncated
			}
			size, hdr = binary.BigEndian.Uint64(b[8:]), 16
		}
		if size < hdr || size > uint64(len(b)) {
			return nil, errTruncated
		}
		if string(b[4:8]) == "moof" {
			return b[hdr:size], nil
		}
		b = b[size:]
	}
	return nil, errors.New("mp4: no moof")
}

// trafTiming sums the samples and durations of one track fragment; ok is
// false for a fragment of another track. A sample's duration comes from the
// trun when it carries one, else tfhd's default, else trex's.
func trafTiming(traf []byte, info InitInfo) (samples, ticks uint64, ok bool, err error) {
	boxes, err := readBoxes(traf)
	if err != nil {
		return 0, 0, false, err
	}
	var defaultDur uint32
	for _, bx := range boxes {
		switch bx.typ {
		case "tfhd":
			b := bx.body
			if len(b) < 8 {
				return 0, 0, false, errTruncated
			}
			if binary.BigEndian.Uint32(b[4:8]) != info.VideoTrackID {
				return 0, 0, false, nil
			}
			ok = true
			flags := binary.BigEndian.Uint32(b[0:4]) & 0xFFFFFF
			off := 8
			if flags&0x01 != 0 { // base_data_offset
				off += 8
			}
			if flags&0x02 != 0 { // sample_description_index
				off += 4
			}
			defaultDur = info.VideoTrexDuration
			if flags&0x08 != 0 { // default_sample_duration
				if len(b) < off+4 {
					return 0, 0, false, errTruncated
				}
				defaultDur = binary.BigEndian.Uint32(b[off : off+4])
			}
		case "trun":
			if !ok {
				return 0, 0, false, errors.New("mp4: trun before tfhd")
			}
			n, d, terr := trunTiming(bx.body, defaultDur)
			if terr != nil {
				return 0, 0, false, terr
			}
			samples += n
			ticks += d
		}
	}
	return samples, ticks, ok, nil
}

func trunTiming(b []byte, defaultDur uint32) (samples, ticks uint64, err error) {
	if len(b) < 8 {
		return 0, 0, errTruncated
	}
	flags := binary.BigEndian.Uint32(b[0:4]) & 0xFFFFFF
	count := uint64(binary.BigEndian.Uint32(b[4:8]))
	off := 8
	if flags&0x001 != 0 { // data_offset
		off += 4
	}
	if flags&0x004 != 0 { // first_sample_flags
		off += 4
	}
	if off > len(b) {
		return 0, 0, errTruncated
	}
	if flags&0x100 == 0 { // no per-sample durations
		if defaultDur == 0 {
			return 0, 0, errors.New("mp4: no sample duration")
		}
		return count, count * uint64(defaultDur), nil
	}
	stride := 4 * bits.OnesCount32(flags&0xF00) // duration, size, flags, composition offset
	if uint64(len(b)-off) < count*uint64(stride) {
		return 0, 0, errTruncated
	}
	for i := uint64(0); i < count; i++ {
		ticks += uint64(binary.BigEndian.Uint32(b[off:]))
		off += stride
	}
	return count, ticks, nil
}

var errTruncated = errors.New("mp4: truncated box")

type mp4Box struct {
	typ  string
	body []byte
}

// readBoxes splits b into consecutive boxes. Every box must fit entirely in
// b: a short read of a file ffmpeg is still writing has to fail rather than
// parse as fewer tracks. Size 0 ("to end of file") is rejected for the same
// reason; ffmpeg never writes it in an init segment.
func readBoxes(b []byte) ([]mp4Box, error) {
	var out []mp4Box
	for len(b) > 0 {
		if len(b) < 8 {
			return nil, errTruncated
		}
		size, hdr := uint64(binary.BigEndian.Uint32(b)), uint64(8)
		if size == 1 {
			if len(b) < 16 {
				return nil, errTruncated
			}
			size, hdr = binary.BigEndian.Uint64(b[8:]), 16
		}
		if size < hdr || size > uint64(len(b)) {
			return nil, errTruncated
		}
		out = append(out, mp4Box{typ: string(b[4:8]), body: b[hdr:size]})
		b = b[size:]
	}
	return out, nil
}

// child returns the body of the first box of type typ directly inside body.
func child(body []byte, typ string) ([]byte, error) {
	boxes, err := readBoxes(body)
	if err != nil {
		return nil, err
	}
	for _, bx := range boxes {
		if bx.typ == typ {
			return bx.body, nil
		}
	}
	return nil, fmt.Errorf("mp4: no %s box", typ)
}

// path follows a chain of box types down from body.
func path(body []byte, types ...string) ([]byte, error) {
	var err error
	for _, t := range types {
		if body, err = child(body, t); err != nil {
			return nil, err
		}
	}
	return body, nil
}

// ParseInitSegment reads the codec and colour information of every track in
// an fMP4 init segment (moov/trak/mdia/minf/stbl/stsd).
func ParseInitSegment(b []byte) (InitInfo, error) {
	moov, err := child(b, "moov")
	if err != nil {
		return InitInfo{}, err
	}
	boxes, err := readBoxes(moov)
	if err != nil {
		return InitInfo{}, err
	}
	var info InitInfo
	trex := map[uint32]uint32{} // track_ID -> default_sample_duration
	if mvex, err := child(moov, "mvex"); err == nil {
		if kids, err := readBoxes(mvex); err == nil {
			for _, k := range kids {
				if k.typ == "trex" && len(k.body) >= 16 {
					trex[binary.BigEndian.Uint32(k.body[4:8])] = binary.BigEndian.Uint32(k.body[12:16])
				}
			}
		}
	}
	codecs, known, tracks := []string{}, true, 0
	for _, bx := range boxes {
		if bx.typ != "trak" {
			continue
		}
		tracks++
		mdia, err := child(bx.body, "mdia")
		if err != nil {
			return InitInfo{}, err
		}
		hdlr, err := child(mdia, "hdlr")
		if err != nil {
			return InitInfo{}, err
		}
		if len(hdlr) < 12 {
			return InitInfo{}, errTruncated
		}
		stsd, err := path(mdia, "minf", "stbl", "stsd")
		if err != nil {
			return InitInfo{}, err
		}
		if len(stsd) < 8 {
			return InitInfo{}, errTruncated
		}
		entries, err := readBoxes(stsd[8:])
		if err != nil {
			return InitInfo{}, err
		}
		if len(entries) == 0 {
			return InitInfo{}, errors.New("mp4: empty stsd")
		}
		var codec string
		switch string(hdlr[8:12]) {
		case "vide":
			var c colour
			codec, c, err = visualCodec(entries[0])
			if err != nil {
				return InitInfo{}, err
			}
			if c.ok && !info.HasColr {
				info.HasColr, info.Transfer = true, c.transfer
			}
			if info.VideoTrackID == 0 {
				// Timing is optional: without it only FRAME-RATE is lost.
				id, idOK := fullBoxField(bx.body, "tkhd", 12, 20)
				ts, tsOK := fullBoxField(mdia, "mdhd", 12, 20)
				if idOK && tsOK {
					info.VideoTrackID, info.VideoTimescale, info.VideoTrexDuration = id, ts, trex[id]
				}
			}
		case "soun":
			if codec, err = audioCodec(entries[0]); err != nil {
				return InitInfo{}, err
			}
		}
		if codec == "" {
			known = false
		}
		codecs = append(codecs, codec)
	}
	if tracks == 0 {
		return InitInfo{}, errors.New("mp4: no tracks")
	}
	if known {
		info.Codecs = codecs
	}
	return info, nil
}

// fullBoxField reads a 32-bit field of the full box typ inside body, at
// offset v0 or v1 depending on the box version (tkhd and mdhd widen their
// time fields to 64 bits in version 1, which moves what follows).
func fullBoxField(body []byte, typ string, v0, v1 int) (uint32, bool) {
	b, err := child(body, typ)
	if err != nil || len(b) < 4 {
		return 0, false
	}
	off := v0
	if b[0] == 1 {
		off = v1
	}
	if len(b) < off+4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(b[off:]), true
}

type colour struct {
	ok       bool
	transfer int
}

// visualEntryHeader is SampleEntry (8 bytes) plus VisualSampleEntry's fixed
// fields (70 bytes); child boxes follow.
const visualEntryHeader = 78

// visualCodec names a video sample entry ("" when it can't) and reads its
// colr box. A malformed entry is an error; an unrecognised one is not.
func visualCodec(e mp4Box) (string, colour, error) {
	if len(e.body) < visualEntryHeader {
		return "", colour{}, errTruncated
	}
	kids, err := readBoxes(e.body[visualEntryHeader:])
	if err != nil {
		return "", colour{}, err
	}
	var c colour
	var codec string
	for _, k := range kids {
		switch {
		case k.typ == "colr" && len(k.body) >= 10 && !c.ok:
			if kind := string(k.body[:4]); kind == "nclx" || kind == "nclc" {
				c = colour{ok: true, transfer: int(binary.BigEndian.Uint16(k.body[6:8]))}
			}
		case k.typ == "hvcC" && (e.typ == "hvc1" || e.typ == "hev1"):
			codec = hevcCodec(e.typ, k.body)
		case k.typ == "avcC" && (e.typ == "avc1" || e.typ == "avc3"):
			if len(k.body) >= 4 {
				codec = fmt.Sprintf("%s.%02x%02x%02x", e.typ, k.body[1], k.body[2], k.body[3])
			}
		}
	}
	return codec, c, nil
}

// hevcCodec builds the codec string of ISO/IEC 14496-15 Annex E from an
// HEVCDecoderConfigurationRecord: profile space and profile, the 32
// compatibility flags in reverse bit order, tier and level, then the six
// constraint bytes with trailing zero bytes dropped.
func hevcCodec(fourcc string, rec []byte) string {
	if len(rec) < 13 {
		return ""
	}
	space := rec[1] >> 6
	tier := "L"
	if rec[1]&0x20 != 0 {
		tier = "H"
	}
	s := fourcc + "."
	if space > 0 {
		s += string(rune('A' + space - 1))
	}
	s += strconv.Itoa(int(rec[1]&0x1F)) + "."
	s += strings.ToUpper(strconv.FormatUint(uint64(bits.Reverse32(binary.BigEndian.Uint32(rec[2:6]))), 16)) + "."
	s += tier + strconv.Itoa(int(rec[12]))
	constraint := rec[6:12]
	n := len(constraint)
	for n > 0 && constraint[n-1] == 0 {
		n--
	}
	for _, c := range constraint[:n] {
		s += "." + strings.ToUpper(strconv.FormatUint(uint64(c), 16))
	}
	return s
}

// audioCodec names an audio sample entry, or returns "" when it can't.
func audioCodec(e mp4Box) (string, error) {
	const head = 28 // SampleEntry + AudioSampleEntry's fixed fields
	if len(e.body) < head {
		return "", errTruncated
	}
	off := head
	switch binary.BigEndian.Uint16(e.body[8:10]) { // QuickTime sound description version
	case 1:
		off += 16
	case 2:
		off += 36
	}
	if len(e.body) < off {
		return "", errTruncated
	}
	switch e.typ {
	case "ac-3", "ec-3":
		return e.typ, nil
	case "mp4a":
		kids, err := readBoxes(e.body[off:])
		if err != nil {
			return "", err
		}
		for _, k := range kids {
			if k.typ == "esds" {
				return mp4aCodec(k.body), nil
			}
		}
	}
	return "", nil
}

// mp4aCodec reads an esds box (ES_Descriptor > DecoderConfigDescriptor >
// DecoderSpecificInfo) and returns "mp4a.40.<audio object type>" for MPEG-4
// audio, "" for anything else or anything malformed.
func mp4aCodec(esds []byte) string {
	if len(esds) < 4 {
		return ""
	}
	tag, es, _, ok := readDescriptor(esds[4:])
	if !ok || tag != 0x03 || len(es) < 3 {
		return ""
	}
	flags := es[2]
	es = es[3:]
	if flags&0x80 != 0 { // streamDependenceFlag: dependsOn_ES_ID
		if len(es) < 2 {
			return ""
		}
		es = es[2:]
	}
	if flags&0x40 != 0 { // URL_Flag: URLlength, URLstring
		if len(es) < 1 || len(es) < 1+int(es[0]) {
			return ""
		}
		es = es[1+int(es[0]):]
	}
	if flags&0x20 != 0 { // OCRstreamFlag: OCR_ES_Id
		if len(es) < 2 {
			return ""
		}
		es = es[2:]
	}
	tag, dcd, _, ok := readDescriptor(es)
	if !ok || tag != 0x04 || len(dcd) < 13 || dcd[0] != 0x40 {
		return ""
	}
	tag, asc, _, ok := readDescriptor(dcd[13:])
	if !ok || tag != 0x05 || len(asc) < 1 {
		return ""
	}
	aot := int(asc[0] >> 3)
	if aot == 31 {
		if len(asc) < 2 {
			return ""
		}
		aot = 32 + (int(asc[0]&0x07)<<3 | int(asc[1]>>5))
	}
	if aot == 0 {
		return ""
	}
	return "mp4a.40." + strconv.Itoa(aot)
}

// readDescriptor reads one MPEG-4 descriptor: a tag byte, then a size in up
// to four bytes of seven bits each (high bit = more follows).
func readDescriptor(b []byte) (tag byte, payload, rest []byte, ok bool) {
	if len(b) < 2 {
		return 0, nil, nil, false
	}
	tag = b[0]
	size, i := 0, 1
	for ; i <= 4; i++ {
		if i >= len(b) {
			return 0, nil, nil, false
		}
		size = size<<7 | int(b[i]&0x7F)
		if b[i]&0x80 == 0 {
			break
		}
	}
	if i > 4 {
		return 0, nil, nil, false
	}
	start := i + 1
	if start+size > len(b) {
		return 0, nil, nil, false
	}
	return tag, b[start : start+size], b[start+size:], true
}
