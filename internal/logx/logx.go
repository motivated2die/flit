// Package logx writes flit's log file. Lines leave out personal details
// (the user's name in paths, the computer's name, e-mail addresses), and
// transcript text must never be logged at all; callers log counts and
// times, not words.
package logx

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

const (
	fileName      = "debug.log"
	oldName       = "debug.old.log"
	crashFileName = "crash.log"
	crashOldName  = "crash.old.log"
	maxSize       = 2 << 20
)

var (
	mu  sync.Mutex
	out io.Writer = os.Stderr
	f   *os.File
	scr = newScrubber(userNames(), os.Getenv("COMPUTERNAME"))

	recent     [recentLines]string // ring of the last lines, for the debug view
	recentNext int
	recentN    int
)

const recentLines = 200

// Init logs to dir\debug.log (moving a file over 2 MB to debug.old.log
// first) and, when attached to a console, to stderr too. The returned
// function closes the file.
func Init(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}, err
	}
	path := filepath.Join(dir, fileName)
	if st, err := os.Stat(path); err == nil && st.Size() > maxSize {
		_ = os.Rename(path, filepath.Join(dir, oldName))
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}, err
	}
	mu.Lock()
	f = file
	if hasConsole() {
		out = io.MultiWriter(file, os.Stderr)
	} else {
		out = file
	}
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		out = os.Stderr
		f.Close()
		f = nil
	}, nil
}

// Printf writes one scrubbed, timestamped line.
func Printf(format string, args ...any) {
	line := scr.scrub(fmt.Sprintf(format, args...))
	ts := time.Now().Format("2006-01-02 15:04:05.000")
	line = ts + " " + strings.TrimRight(line, "\n")
	mu.Lock()
	defer mu.Unlock()
	fmt.Fprintln(out, line)
	recent[recentNext] = line
	recentNext = (recentNext + 1) % recentLines
	recentN = min(recentN+1, recentLines)
}

// Recent returns up to n of the latest lines, oldest first.
func Recent(n int) []string {
	mu.Lock()
	defer mu.Unlock()
	n = min(n, recentN)
	lines := make([]string, n)
	for i := range lines {
		lines[i] = recent[(recentNext-n+i+recentLines)%recentLines]
	}
	return lines
}

// Path returns the log file's path in dir.
func Path(dir string) string { return filepath.Join(dir, fileName) }

// CrashPath returns the crash log file's path in dir.
func CrashPath(dir string) string { return filepath.Join(dir, crashFileName) }

// CrashInfo reports whether a non-empty crash log exists in dir, along with
// its size in bytes and last modification time.
func CrashInfo(dir string) (size int64, mod time.Time, ok bool) {
	st, err := os.Stat(CrashPath(dir))
	if err != nil || st.Size() == 0 {
		return 0, time.Time{}, false
	}
	return st.Size(), st.ModTime(), true
}

// InitCrash routes Go runtime crash output (unrecovered panics, fatal runtime
// errors, and unhandled exceptions) to dir\crash.log, rotating to
// crash.old.log once it exceeds maxSize. This preserves full goroutine stack
// traces even in GUI builds where stderr is disconnected.
// The returned function clears the crash output and closes the file.
func InitCrash(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return func() {}, err
	}
	path := CrashPath(dir)
	if st, err := os.Stat(path); err == nil && st.Size() > maxSize {
		_ = os.Rename(path, filepath.Join(dir, crashOldName))
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}, err
	}
	if err := debug.SetCrashOutput(file, debug.CrashOptions{}); err != nil {
		_ = file.Close()
		return func() {}, err
	}
	return func() {
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		_ = file.Close()
	}, nil
}

func hasConsole() bool {
	st, err := os.Stderr.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func userNames() []string {
	var names []string
	for _, v := range []string{"USERNAME", "USERPROFILE"} {
		if s := os.Getenv(v); s != "" {
			names = append(names, filepath.Base(s))
		}
	}
	return names
}

type scrubber struct {
	names    []string
	computer string
	email    *regexp.Regexp
	profile  *regexp.Regexp
}

func newScrubber(names []string, computer string) *scrubber {
	return &scrubber{
		names:    names,
		computer: computer,
		email:    regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
		profile:  regexp.MustCompile(`(?i)(\\Users\\)[^\\/"']+`),
	}
}

// scrub removes the user's names, the computer's name and e-mail
// addresses from s.
func (s *scrubber) scrub(text string) string {
	text = s.email.ReplaceAllString(text, "<email>")
	text = s.profile.ReplaceAllString(text, `${1}<user>`)
	for _, n := range s.names {
		if len(n) >= 3 {
			text = replaceFold(text, n, "<user>")
		}
	}
	if len(s.computer) >= 3 {
		text = replaceFold(text, s.computer, "<computer>")
	}
	return text
}

func replaceFold(s, old, repl string) string {
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(old) + `\b`)
	return re.ReplaceAllString(s, repl)
}
