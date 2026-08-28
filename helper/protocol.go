package main

import (
	"encoding/json"
	"io"
	"sync"
)

var emitMu sync.Mutex

// Emit writes one JSON event line. The JS side splits helper stdout on
// newlines, so an event must never span or share a line.
func Emit(w io.Writer, event string, fields map[string]any) {
	obj := map[string]any{"event": event}
	for k, v := range fields {
		obj[k] = v
	}
	b, err := json.Marshal(obj)
	if err != nil {
		b = []byte(`{"event":"error","msg":"marshal failure"}`)
	}
	emitMu.Lock()
	defer emitMu.Unlock()
	w.Write(append(b, '\n'))
}
