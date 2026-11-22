// Package calendar reads calendar subscription feeds (iCalendar / ICS,
// RFC 5545): the events of the next days with their times, places and
// meeting links, expanded from recurring series, and matches recordings to
// them. See docs/architecture/calendar.md.
//
// The parser reads only what flit uses and is bounded in every direction
// (feed size, components, recurrence steps), so a strange or hostile feed
// can't make it hang or grow.
package calendar

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Limits on what a feed may contain.
const (
	maxFeedBytes  = 10 << 20
	maxComponents = 50000
	maxLineBytes  = 1 << 20
)

// ErrNotCalendar means the data isn't an iCalendar feed (a login page, an
// error page, a wrong link).
var ErrNotCalendar = errors.New("not a calendar feed (no BEGIN:VCALENDAR)")

// prop is one content line: NAME;PARAM=value:VALUE.
type prop struct {
	name   string            // upper case
	params map[string]string // upper-case keys, unquoted values
	value  string            // raw (escapes not yet resolved)
}

// component is BEGIN:NAME … END:NAME with its properties and children.
type component struct {
	name     string
	props    []prop
	children []*component
}

// get returns the first property called name.
func (c *component) get(name string) (prop, bool) {
	for _, p := range c.props {
		if p.name == name {
			return p, true
		}
	}
	return prop{}, false
}

// all returns every property called name.
func (c *component) all(name string) []prop {
	var out []prop
	for _, p := range c.props {
		if p.name == name {
			out = append(out, p)
		}
	}
	return out
}

// parse reads a feed into its VCALENDAR component.
func parse(r io.Reader) (*component, error) {
	lines, err := unfold(io.LimitReader(r, maxFeedBytes+1))
	if err != nil {
		return nil, err
	}
	var root *component
	var stack []*component
	count := 0
	for _, line := range lines {
		p, ok := parseLine(line)
		if !ok {
			continue // a line that isn't a property: skip it, as clients do
		}
		switch p.name {
		case "BEGIN":
			count++
			if count > maxComponents {
				return nil, errors.New("calendar feed too large (too many entries)")
			}
			c := &component{name: strings.ToUpper(p.value)}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.children = append(parent.children, c)
			} else if root == nil && c.name == "VCALENDAR" {
				root = c
			}
			stack = append(stack, c)
		case "END":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		default:
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.props = append(top.props, p)
			}
		}
	}
	if root == nil {
		return nil, ErrNotCalendar
	}
	return root, nil
}

// unfold joins folded lines (a line break followed by a space or tab
// continues the line) and accepts CRLF or LF.
func unfold(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxLineBytes)
	var lines []string
	var cur strings.Builder
	total := 0
	for sc.Scan() {
		raw := sc.Text()
		total += len(raw) + 1
		if total > maxFeedBytes {
			return nil, errors.New("calendar feed larger than 10 MB")
		}
		raw = strings.TrimSuffix(raw, "\r")
		if len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t') {
			cur.WriteString(raw[1:])
			continue
		}
		if cur.Len() > 0 {
			lines = append(lines, cur.String())
			cur.Reset()
		}
		cur.WriteString(raw)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	if len(lines) > 0 {
		lines[0] = strings.TrimPrefix(lines[0], "\uFEFF") // a byte-order mark
	}
	return lines, nil
}

// parseLine splits NAME;P1=V1;P2="V,2":VALUE. The value starts at the
// first colon outside quotes.
func parseLine(line string) (prop, bool) {
	inQuote := false
	colon := -1
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inQuote = !inQuote
		case ':':
			if !inQuote {
				colon = i
			}
		}
		if colon >= 0 {
			break
		}
	}
	if colon <= 0 {
		return prop{}, false
	}
	head, value := line[:colon], line[colon+1:]
	parts := splitOutsideQuotes(head, ';')
	p := prop{name: strings.ToUpper(strings.TrimSpace(parts[0])), value: value}
	for _, kv := range parts[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if p.params == nil {
			p.params = map[string]string{}
		}
		p.params[strings.ToUpper(strings.TrimSpace(k))] = strings.Trim(v, `"`)
	}
	return p, p.name != ""
}

func splitOutsideQuotes(s string, sep byte) []string {
	var out []string
	inQuote := false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuote = !inQuote
		case sep:
			if !inQuote {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// text resolves a TEXT value's escapes: \n, \N, \, \; \\.
func text(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
			switch v[i] {
			case 'n', 'N':
				b.WriteByte('\n')
			default:
				b.WriteByte(v[i])
			}
			continue
		}
		b.WriteByte(v[i])
	}
	return b.String()
}
