package autosnap

import (
	"fmt"
	"io"
	"sync"
	"time"
)

type pendingDebugLogger struct {
	enabled bool
	out     io.Writer
	start   time.Time
	mu      *sync.Mutex
}

func newPendingDebugLogger(out io.Writer, enabled bool) pendingDebugLogger {
	return pendingDebugLogger{
		enabled: enabled,
		out:     out,
		start:   time.Now(),
		mu:      &sync.Mutex{},
	}
}

func (d pendingDebugLogger) Printf(format string, args ...any) {
	if !d.enabled || d.out == nil {
		return
	}
	elapsed := time.Since(d.start).Round(time.Millisecond)
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintf(d.out, "debug: pending: +%s "+format+"\n", append([]any{elapsed}, args...)...)
}
