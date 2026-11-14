package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func appendPGSSegment(dst []byte, pts uint32, kind byte, payload []byte) []byte {
	dst = append(dst, 'P', 'G')
	var header [11]byte
	binary.BigEndian.PutUint32(header[0:4], pts)
	header[8] = kind
	binary.BigEndian.PutUint16(header[9:11], uint16(len(payload)))
	dst = append(dst, header[:]...)
	return append(dst, payload...)
}

func pgsPCS(number uint16, state byte, visible bool) []byte {
	b := new(bytes.Buffer)
	for _, n := range []uint16{640, 360} {
		_ = binary.Write(b, binary.BigEndian, n)
	}
	b.WriteByte(0x10)
	_ = binary.Write(b, binary.BigEndian, number)
	b.Write([]byte{state, 0, 0})
	if visible {
		b.WriteByte(1)
		_ = binary.Write(b, binary.BigEndian, uint16(0)) // object id
		b.Write([]byte{0, 0})                            // window id, flags
		_ = binary.Write(b, binary.BigEndian, uint16(200))
		_ = binary.Write(b, binary.BigEndian, uint16(120))
	} else {
		b.WriteByte(0)
	}
	return b.Bytes()
}

// makePGSFixture creates an eight-second black H.264 clip with a synthetic
// opaque 20x20 PGS square from 1s to 5s, so the cast's burned pixels are exact.
func makePGSFixture(t *testing.T, ffmpeg string) string {
	t.Helper()
	dir := t.TempDir()
	supPath := filepath.Join(dir, "square.sup")
	var sup []byte
	const ptsShow = uint32(90000)
	const ptsClear = uint32(450000)
	var window bytes.Buffer
	window.WriteByte(1)
	window.WriteByte(0)
	for _, n := range []uint16{0, 0, 640, 360} {
		_ = binary.Write(&window, binary.BigEndian, n)
	}
	palette := []byte{0, 0, 1, 235, 128, 128, 255}    // opaque white at entry 1
	rle := bytes.Repeat([]byte{0, 0x94, 1, 0, 0}, 20) // 20 white pixels, end of each row
	object := new(bytes.Buffer)
	_ = binary.Write(object, binary.BigEndian, uint16(0))
	object.Write([]byte{0, 0xc0}) // object 0, first and last fragment
	dataLength := uint32(4 + len(rle))
	object.Write([]byte{byte(dataLength >> 16), byte(dataLength >> 8), byte(dataLength)})
	_ = binary.Write(object, binary.BigEndian, uint16(20))
	_ = binary.Write(object, binary.BigEndian, uint16(20))
	object.Write(rle)
	sup = appendPGSSegment(sup, ptsShow, 0x16, pgsPCS(1, 0x80, true))
	sup = appendPGSSegment(sup, ptsShow, 0x17, window.Bytes())
	sup = appendPGSSegment(sup, ptsShow, 0x14, palette)
	sup = appendPGSSegment(sup, ptsShow, 0x15, object.Bytes())
	sup = appendPGSSegment(sup, ptsShow, 0x80, nil)
	sup = appendPGSSegment(sup, ptsClear, 0x16, pgsPCS(2, 0, false))
	sup = appendPGSSegment(sup, ptsClear, 0x80, nil)
	if err := os.WriteFile(supPath, sup, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "image-subtitle.mkv")
	args := []string{"-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=black:size=640x360:rate=25:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8", "-i", supPath,
		"-map", "0:v", "-map", "1:a", "-map", "2:s",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-c:s", "copy", fixture}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("PGS fixture: %v\n%s", err, out)
	}
	return fixture
}

func waitForPackaged(t *testing.T, stdout io.Reader) string {
	t.Helper()
	lines := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	var playlist string
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("helper stdout closed before packaging")
			}
			var event struct {
				Event string `json:"event"`
				URL   string `json:"url"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatalf("invalid helper event %q: %v", line, err)
			}
			switch event.Event {
			case "ready":
				playlist = event.URL
			case "error":
				t.Fatalf("helper reported error: %s", event.Error)
			case "packaged":
				if playlist == "" {
					t.Fatal("helper packaged before announcing playlist")
				}
				return playlist
			}
		case <-timer.C:
			t.Fatal("timed out waiting for helper packaging")
		}
	}
}

func makeVobSubFixture(t *testing.T, ffmpeg string) string {
	t.Helper()
	pgs := makePGSFixture(t, ffmpeg)
	dir := t.TempDir()
	vobsub := filepath.Join(dir, "vobsub-only.mkv")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", pgs,
		"-map", "0:s:0", "-c:s", "dvdsub", vobsub}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("create VobSub fixture: %v\\n%s", err, out)
	}
	fixture := filepath.Join(dir, "image-subtitle-vobsub.mkv")
	args = []string{"-hide_banner", "-loglevel", "error", "-y", "-i", pgs, "-i", vobsub,
		"-map", "0:v:0", "-map", "0:a:0", "-map", "1:s:0",
		"-c:v", "copy", "-c:a", "copy", "-c:s", "copy", fixture}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("mux VobSub fixture: %v\\n%s", err, out)
	}
	return fixture
}

func TestSystemFFmpegBurnsPGSSubtitlePixels(t *testing.T) {
	ffmpeg := findSystemFFmpeg(t)
	fixture := makePGSFixture(t, ffmpeg)
	output := filepath.Join(t.TempDir(), "burned.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", fixture,
		"-filter_complex", "[0:0][0:2]overlay[burned]", "-map", "[burned]", "-map", "0:1",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "copy", "-sn", output}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("system ffmpeg PGS burn-in: %v\\n%s", err, out)
	}
	for _, sample := range []struct {
		name   string
		at     string
		x, y   string
		bright bool
	}{
		{name: "subtitle visible", at: "2", x: "210", y: "130", bright: true},
		{name: "unaffected video", at: "2", x: "100", y: "100"},
		{name: "subtitle cleared", at: "6", x: "210", y: "130"},
	} {
		crop := fmt.Sprintf("crop=2:2:%s:%s", sample.x, sample.y)
		pixelArgs := []string{"-hide_banner", "-loglevel", "error", "-ss", sample.at, "-i", output,
			"-vf", crop, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1"}
		pixels, err := exec.Command(ffmpeg, pixelArgs...).Output()
		if err != nil {
			t.Fatalf("decode %s: %v", sample.name, err)
		}
		if len(pixels) != 12 {
			t.Fatalf("%s produced %d bytes, want a 2x2 RGB patch", sample.name, len(pixels))
		}
		for _, channel := range pixels[:3] {
			if sample.bright && channel < 180 || !sample.bright && channel > 20 {
				t.Fatalf("%s pixel = %v", sample.name, pixels)
			}
		}
	}
}

func TestSystemFFmpegBurnsVobSubSubtitlePixels(t *testing.T) {
	ffmpeg := findSystemFFmpeg(t)
	fixture := makeVobSubFixture(t, ffmpeg)
	output := filepath.Join(t.TempDir(), "burned-vobsub.mp4")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", fixture,
		"-filter_complex", "[0:0][0:2]overlay[burned]", "-map", "[burned]", "-map", "0:1",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "copy", "-sn", output}
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("system ffmpeg VobSub burn-in: %v\\n%s", err, out)
	}
	args = []string{"-hide_banner", "-loglevel", "error", "-ss", "2", "-i", output,
		"-vf", "crop=2:2:210:130", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1"}
	pixels, err := exec.Command(ffmpeg, args...).Output()
	if err != nil {
		t.Fatalf("decode burned VobSub frame: %v", err)
	}
	if len(pixels) != 12 || pixels[0] < 180 || pixels[1] < 180 || pixels[2] < 180 {
		t.Fatalf("VobSub pixel at (210,130) is not opaque white: %v", pixels)
	}
}

func TestRealFFmpegBurnsImageSubtitlePixels(t *testing.T) {
	ffmpeg := findFFmpeg(t)
	encoders, _ := exec.Command(ffmpeg, "-hide_banner", "-encoders").CombinedOutput()
	if !strings.Contains(string(encoders), "hevc_videotoolbox") {
		if os.Getenv("IINA_AIRPLAY_FFMPEG") != "" {
			t.Fatal("packaged burn-in gate requires hevc_videotoolbox")
		}
		t.Skip("hevc_videotoolbox unavailable; packaged burn-in gate runs on macOS")
	}
	bin := buildHelper(t)
	cases := []struct {
		name    string
		fixture func(*testing.T, string) string
	}{
		{name: "PGS", fixture: makePGSFixture},
		{name: "VobSub", fixture: makeVobSubFixture},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			systemFFmpeg := findSystemFFmpeg(t)
			fixture := tc.fixture(t, systemFFmpeg)
			out := t.TempDir()
			cmd := exec.Command(bin, "serve", "-source", fixture, "-out", out, "-ffmpeg", ffmpeg,
				"-parent", fmt.Sprint(os.Getpid()), "-duration", "8", "-vcodec", "h264", "-acodec", "aac",
				"-achannels", "2", "-vmap", "0", "-amap", "1", "-burninsmap", "2")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			playlist := waitForPackaged(t, stdout)
			if !strings.HasSuffix(playlist, "/index.m3u8") {
				t.Fatalf("image subtitles must be burned into the video playlist, got %q", playlist)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			decodePixel := func(x, y string) []byte {
				t.Helper()
				args := []string{"-hide_banner", "-loglevel", "error", "-ss", "2", "-i", playlist,
					"-vf", fmt.Sprintf("crop=2:2:%s:%s", x, y), "-frames:v", "1",
					"-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1"}
				pixels, err := exec.CommandContext(ctx, systemFFmpeg, args...).Output()
				if err != nil {
					t.Fatalf("decode HLS pixel at (%s,%s): %v", x, y, err)
				}
				return pixels
			}
			pixels := decodePixel("210", "130")
			if len(pixels) != 12 || pixels[0] < 180 || pixels[1] < 180 || pixels[2] < 180 {
				t.Fatalf("subtitle pixel at (210,130) is not opaque white: %v", pixels)
			}
			outside := decodePixel("100", "100")
			if len(outside) != 12 || outside[0] > 20 || outside[1] > 20 || outside[2] > 20 {
				t.Fatalf("burn-in altered an outside pixel: %v", outside)
			}
		})
	}
}
