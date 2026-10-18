//go:build integration

package pipelinetest

import (
	"encoding/json"
	"runtime"
	"time"
)

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// waitBriefly yields real time to the executor goroutine between drain checks. The fake clock
// doesn't move here; only Advance moves it.
func waitBriefly() {
	runtime.Gosched()
	time.Sleep(2 * time.Millisecond)
}
