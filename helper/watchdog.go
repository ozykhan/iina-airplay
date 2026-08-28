package main

import "time"

// WatchParent polls rather than using kqueue: ~nothing at 2s intervals, and
// it works no matter how the parent died (quit, crash, kill -9).
func WatchParent(pid int, onGone func()) {
	go func() {
		for {
			time.Sleep(2 * time.Second)
			if !processAlive(pid) {
				onGone()
				return
			}
		}
	}()
}
