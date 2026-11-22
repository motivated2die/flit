package ui

import (
	"testing"
	"time"
)

func TestEditing(t *testing.T) {
	e := &Editing{Text: []rune("Speaker 2"), Caret: 9, All: true, Max: 12}
	e.Insert("Anna") // replaces the selection
	if string(e.Text) != "Anna" || e.Caret != 4 {
		t.Fatalf("%q %d", string(e.Text), e.Caret)
	}
	e.Move(0)
	e.Insert("Dr.\n")
	if string(e.Text) != "Dr. Anna" || e.Caret != 4 {
		t.Fatalf("%q %d", string(e.Text), e.Caret)
	}
	e.Move(99)
	e.Insert(" Müller-Lüdenscheidt") // cut at Max
	if string(e.Text) != "Dr. Anna Mül" {
		t.Fatalf("max: %q", string(e.Text))
	}
	e.Backspace()
	e.Move(0)
	e.Delete()
	if string(e.Text) != "r. Anna Mü" || e.Caret != 0 {
		t.Fatalf("%q %d", string(e.Text), e.Caret)
	}
	e.All = true
	e.Delete()
	if len(e.Text) != 0 {
		t.Fatal("selection not deleted")
	}
}

func TestTextFieldRender(t *testing.T) {
	m, err := D2DMetrics()
	if err != nil {
		t.Skip(err)
	}
	var started string
	var committed string
	field := TextField{ID: "name", Value: "Speaker 1", W: 200, OnCommit: func(s string) { committed = s }}
	c := NewCtx(1, m, time.Now(), "", "", nil)
	c.StartEdit = func(id, value string, _ int) { started = id + "=" + value }
	field.Layout(c, 0, 0, 400)
	hit, ok := c.HitAt(50, 10)
	if !ok || hit.ID != "name" {
		t.Fatal("field not clickable")
	}
	hit.OnClick()
	if started != "name=Speaker 1" {
		t.Fatalf("started %q", started)
	}
	if commit := c.committer("name"); commit == nil {
		t.Fatal("no committer")
	} else {
		commit("Anna")
	}
	if committed != "Anna" {
		t.Fatal("commit")
	}
	// Editing: drawn with a caret and asks for repaints.
	c = NewCtx(1, m, time.Now(), "", "", nil)
	c.Edit = &Editing{ID: "name", Text: []rune("An"), Caret: 2, Since: c.Now}
	field.Layout(c, 0, 0, 400)
	if !c.Animating {
		t.Fatal("caret doesn't blink")
	}
}

func TestMeetingRowHoverActions(t *testing.T) {
	m, err := D2DMetrics()
	if err != nil {
		t.Skip(err)
	}
	row := MeetingRow{ID: "cal:1", Time: "14:00", Until: "15:00", Title: "AG", Place: "Zoom",
		Actions:      []Node{Button{ID: "cal:1:join", Label: "Join", OnClick: func() {}}},
		HoverActions: []Node{Button{ID: "cal:1:record", Label: "Record", OnClick: func() {}}}}
	has := func(hover, id string) bool {
		c := NewCtx(1, m, time.Now(), hover, "", nil)
		row.Layout(c, 0, 0, 600)
		for _, h := range c.hits {
			if h.ID == id {
				return true
			}
		}
		return false
	}
	if has("", "cal:1:record") || !has("", "cal:1:join") {
		t.Fatal("record shown without hover")
	}
	if !has("cal:1", "cal:1:record") || !has("cal:1:join", "cal:1:record") {
		t.Fatal("record not shown on hover (row or its buttons)")
	}
	// The row itself isn't clickable: no hand cursor over it.
	c := NewCtx(1, m, time.Now(), "", "", nil)
	row.Layout(c, 0, 0, 600)
	if h, ok := c.HitAt(150, 20); !ok || h.ID != "cal:1" || h.clickable() {
		t.Fatalf("row hit %+v %v", h, ok)
	}
}
