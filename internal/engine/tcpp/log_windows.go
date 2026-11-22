package tcpp

import (
	"strings"
	"sync/atomic"
)

// LogLevel is a transcribe_log_level.
type LogLevel int

const (
	LogInfo  LogLevel = 1
	LogWarn  LogLevel = 2
	LogError LogLevel = 3
	LogDebug LogLevel = 4
	logCont  LogLevel = 5 // continues the previous message
)

var (
	logger  atomic.Pointer[func(level LogLevel, msg string)]
	lastLvl atomic.Int32
)

// SetLogger receives the engine's log messages (nil: discard). It may be
// called from any thread, including ggml's workers.
func SetLogger(fn func(level LogLevel, msg string)) {
	if fn == nil {
		logger.Store(nil)
		return
	}
	logger.Store(&fn)
}

// logMessage handles one message from the engine. Continuation fragments
// (ggml's progress output) keep the level of the message they continue;
// fragments without text, such as progress dots, are dropped.
func logMessage(level LogLevel, msg string) {
	if level == logCont {
		level = LogLevel(lastLvl.Load())
	} else {
		lastLvl.Store(int32(level))
	}
	msg = strings.TrimRight(msg, "\r\n")
	if strings.Trim(msg, ". \t") == "" {
		return
	}
	if fn := logger.Load(); fn != nil {
		(*fn)(level, msg)
	}
}
