package tcpp

import "golang.org/x/sys/windows"

// logCallback is the C function pointer handed to transcribe_log_set.
var logCallback = windows.NewCallback(func(level, msg, _ uintptr) uintptr {
	logMessage(LogLevel(level), cstr(msg))
	return 0
})

// installLogger hooks the engine's log into SetLogger's function, once and
// before anything else runs, as transcribe.h requires.
func installLogger(l *Lib) {
	l.p.logSet.call(logCallback, 0)
}
