package autosnap

import (
	"fmt"
	"io"
	"os"
	"time"
)

const autosnapTimestampLogEnv = "AUTOSNAP_LOG_TIMESTAMP"

var logTimestampsEnabled = os.Getenv(autosnapTimestampLogEnv) == "1"

func logf(format string, args ...any) {
	if !logTimestampsEnabled {
		fmt.Printf(format, args...)
		return
	}

	allArgs := make([]any, 0, len(args)+1)
	allArgs = append(allArgs, time.Now().Format(time.RFC3339Nano))
	allArgs = append(allArgs, args...)
	fmt.Printf("[%s] "+format, allArgs...)
}

func logln(args ...any) {
	if !logTimestampsEnabled {
		fmt.Println(args...)
		return
	}

	fmt.Printf("[%s] ", time.Now().Format(time.RFC3339Nano))
	fmt.Println(args...)
}

type timestampWriter struct {
	inner io.Writer
}

func (w *timestampWriter) Write(p []byte) (int, error) {
	if !logTimestampsEnabled {
		return w.inner.Write(p)
	}

	if len(p) == 0 {
		return 0, nil
	}

	if _, err := w.inner.Write([]byte("[" + time.Now().Format(time.RFC3339Nano) + "] ")); err != nil {
		return 0, err
	}

	n, err := w.inner.Write(p)
	return n, err
}

func withTimestampWriter(w io.Writer) io.Writer {
	return &timestampWriter{inner: w}
}

