package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEmitWritesOneJSONLine(t *testing.T) {
	var buf bytes.Buffer
	Emit(&buf, "ready", map[string]any{"url": "http://10.0.0.5:49152/index.m3u8"})
	line := buf.String()
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 {
		t.Fatalf("want exactly one newline-terminated line, got %q", line)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got["event"] != "ready" || got["url"] != "http://10.0.0.5:49152/index.m3u8" {
		t.Fatalf("wrong payload: %v", got)
	}
}

func TestEmitNilFields(t *testing.T) {
	var buf bytes.Buffer
	Emit(&buf, "stopped", nil)
	if strings.TrimSpace(buf.String()) != `{"event":"stopped"}` {
		t.Fatalf("got %q", buf.String())
	}
}
