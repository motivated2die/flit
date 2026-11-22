package notes

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// kinds is a doc as "kind/indent text" lines, for comparing.
func kinds(d Doc) []string {
	names := map[Kind]string{Para: "p", H2: "h2", H3: "h3", Bullet: "b", Numbered: "n", Task: "t"}
	var out []string
	for _, b := range d.Blocks {
		k := names[b.Kind]
		if b.Kind == Task && b.Done {
			k = "x"
		}
		if b.Kind.List() {
			k += string(rune('0' + b.Indent))
		}
		out = append(out, k+" "+b.Text)
	}
	return out
}

func TestParse(t *testing.T) {
	md := "# Big\n## Agenda\n### Detail ###\n\nSome text\nNext line\n\n- one\n  - two\n    - three\n* star\n+ plus\n\n1. first\n2) second\n   - under number\n\n- [ ] open\n- [x] done\n  - [X] nested done\n\t- tab nested\n☐ plain box\n☑ plain done\n• dot\n\\- not a bullet\n- - -\n"
	got := kinds(Parse(md))
	want := []string{
		"h2 Big", "h2 Agenda", "h3 Detail",
		"p Some text", "p Next line",
		"b0 one", "b1 two", "b2 three", "b0 star", "b0 plus",
		"n0 first", "n0 second", "b1 under number",
		"t0 open", "x0 done", "x1 nested done", "b2 tab nested",
		"t0 plain box", "x0 plain done", "b0 dot",
		"p - not a bullet", "p - - -",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Parse:\n got %q\nwant %q", got, want)
	}
}

func TestParseContinuation(t *testing.T) {
	got := kinds(Parse("- item\n  continued here\nnot continued\n"))
	want := []string{"b0 item continued here", "p not continued"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestMarkdown(t *testing.T) {
	d := Doc{Blocks: []Block{
		{Kind: H2, Text: "Agenda"},
		{Kind: Para, Text: "Intro with **bold**"},
		{Kind: Para, Text: "- looks like a bullet"},
		{Kind: Bullet, Text: "a"},
		{Kind: Bullet, Indent: 1, Text: "b"},
		{Kind: Numbered, Indent: 1, Text: "c"},
		{Kind: Numbered, Indent: 1, Text: "d"},
		{Kind: Bullet, Indent: 2, Text: "e"},
		{Kind: Task, Text: "task"},
		{Kind: Task, Indent: 1, Done: true, Text: "sub"},
		{Kind: H3, Text: "Next"},
		{Kind: Numbered, Text: "one"},
		{Kind: Numbered, Text: "two"},
	}}
	want := "## Agenda\n\nIntro with **bold**\n\n\\- looks like a bullet\n\n" +
		"- a\n  - b\n  1. c\n  2. d\n     - e\n- [ ] task\n  - [x] sub\n\n" +
		"### Next\n\n1. one\n2. two\n"
	if got := d.Markdown(); got != want {
		t.Errorf("Markdown:\n%s\nwant:\n%s", got, want)
	}
	// And back: the same blocks.
	if got, w := kinds(Parse(want)), kinds(d); !reflect.DeepEqual(got, w) {
		t.Errorf("round trip:\n got %q\nwant %q", got, w)
	}
}

func TestRoundTripStable(t *testing.T) {
	for _, md := range []string{
		"",
		"Just text\n",
		"## A\n\n- x\n  - y\n    - z\n- w\n",
		"1. a\n   1. b\n   2. c\n2. d\n",
		"- [ ] a\n  - [x] b\n\n10. ten\n11. eleven\n",
		"\\# not a heading\n\n\\1. not a number\n",
		"[@flit 12:40: check the budget] and *more*\n",
	} {
		once := Parse(md).Markdown()
		twice := Parse(once).Markdown()
		if once != twice {
			t.Errorf("not stable for %q:\n%q\n%q", md, once, twice)
		}
	}
}

func TestNumbersRestart(t *testing.T) {
	d := Parse("1. a\n2. b\n\nText\n\n1. c\n- bullet\n1. d\n")
	if got, want := d.Numbers(), []int{1, 2, 0, 1, 0, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("Numbers = %v, want %v", got, want)
	}
}

func TestPlainAndHTML(t *testing.T) {
	d := Parse("## Plan\n\nWe **ship** on *Friday*\n\n- a\n  - b\n1. one\n- [ ] todo\n- [x] done\n\n[@flit 3:05: what was decided?]\n")
	wantPlain := "Plan\n\nWe ship on Friday\n\n• a\n  • b\n1. one\n☐ todo\n☑ done\n\n@flit (3:05): what was decided?\n"
	if got := d.Plain(); got != wantPlain {
		t.Errorf("Plain:\n%q\nwant\n%q", got, wantPlain)
	}
	wantHTML := "<h2>Plan</h2><p>We <strong>ship</strong> on <em>Friday</em></p>" +
		"<ul><li>a<ul><li>b</li></ul></li></ul><ol><li>one</li></ol><ul><li>☐ todo</li><li>☑ done</li></ul>" +
		"<p>@flit (3:05): what was decided?</p>"
	if got := d.HTML(); got != wantHTML {
		t.Errorf("HTML:\n%s\nwant\n%s", got, wantHTML)
	}
	// Plain text pasted back keeps the structure.
	if got, want := kinds(Parse(wantPlain)), []string{"p Plan", "p We ship on Friday", "b0 a", "b1 b", "n0 one", "t0 todo", "x0 done", "p @flit (3:05): what was decided?"}; !reflect.DeepEqual(got, want) {
		t.Errorf("plain back: %q", got)
	}
}

func TestHTMLEscapes(t *testing.T) {
	if got := Parse("a < b & **c**\n").HTML(); got != "<p>a &lt; b &amp; <strong>c</strong></p>" {
		t.Errorf("HTML = %s", got)
	}
}

// styled renders spans compactly: *bold*, _italic_, [marker], {mention parts}.
func styled(s string) string {
	var sb strings.Builder
	for _, sp := range Inline(s) {
		t := s[sp.From:sp.To]
		switch sp.Role {
		case Marker:
			sb.WriteString("[" + t + "]")
		case Mention, Meta, Note:
			sb.WriteString("{" + t + "}")
		default:
			switch {
			case sp.Bold && sp.Italic:
				sb.WriteString("BI(" + t + ")")
			case sp.Bold:
				sb.WriteString("B(" + t + ")")
			case sp.Italic:
				sb.WriteString("I(" + t + ")")
			default:
				sb.WriteString(t)
			}
		}
	}
	return sb.String()
}

func TestInline(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                  "plain",
		"a **b** c":              "a [**]B(b)[**] c",
		"a *b* c":                "a [*]I(b)[*] c",
		"__b__ and _i_":          "[__]B(b)[__] and [_]I(i)[_]",
		"**bold *both* bold**":   "[**]B(bold )[*]BI(both)[*]B( bold)[**]",
		"snake_case_name":        "snake_case_name",
		"2 * 3 * 4":              "2 * 3 * 4",
		"**open only":            "**open only",
		"x ** y":                 "x ** y",
		"[@flit: check] after":   "[[]{@flit}{: }{check}[]] after",
		"[@flit 1:02:03: q] *i*": "[[]{@flit}{ 1:02:03: }{q}[]] [*]I(i)[*]",
		"[@flit:]":               "[[]{@flit}{:}[]]",
	} {
		if got := styled(in); got != want {
			t.Errorf("Inline(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInlineCovers(t *testing.T) {
	for _, s := range []string{"a **b** *c* d", "[@flit 2:00: x] **y**", "***", "**", "*a**b*"} {
		at := 0
		for _, sp := range Inline(s) {
			if sp.From != at || sp.To <= sp.From {
				t.Fatalf("%q: span %+v leaves a gap or is empty (at %d)", s, sp, at)
			}
			at = sp.To
		}
		if at != len(s) {
			t.Errorf("%q: spans end at %d of %d", s, at, len(s))
		}
	}
}

func TestMentions(t *testing.T) {
	m := Mentions("a [@flit 12:40: is the budget final?] b [@flit: later]")
	if len(m) != 2 || m[0].At != 12*time.Minute+40*time.Second || m[0].Instruction != "is the budget final?" || m[1].At != Unknown || m[1].Instruction != "later" {
		t.Errorf("Mentions = %+v", m)
	}
	if got := Clock(time.Hour + 2*time.Minute + 3*time.Second); got != "1:02:03" {
		t.Errorf("Clock = %s", got)
	}
}
