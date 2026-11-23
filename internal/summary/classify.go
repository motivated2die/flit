package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/motivated2die/flit/internal/llm"
)

// MeetingClass is a lightweight classification of a meeting's style and
// information density, produced by a tiny constrained JSON call on the
// already-loaded model (AnyJev-style: ~500 tokens read, ~20 tokens written).
type MeetingClass struct {
	Type    string `json:"type"`    // one_on_one, standup_sync, decision_planning, brainstorm, interview_chat
	Density string `json:"density"` // low, medium, high
}

const (
	classifyMax    = 48
	classifyWrites = 24
)

func classifySchema() json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"type": map[string]any{
				"type": "string",
				"enum": []string{"one_on_one", "standup_sync", "decision_planning", "brainstorm", "interview_chat"},
			},
			"density": map[string]any{
				"type": "string",
				"enum": []string{"low", "medium", "high"},
			},
		},
		"required":             []string{"type", "density"},
		"additionalProperties": false,
	})
	return b
}

// classify inspects a compact sample (start, middle, end) of the meeting
// using the already-loaded model to steer the writer's structure and length.
func classify(ctx context.Context, m Model, o Options, head string, ts []Turn, progress func(llm.CallProgress)) (MeetingClass, Call, string, error) {
	sample := sampleTranscript(ts, 1800) // ~550 tokens max
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("Sample excerpts from the start, middle, and end of the meeting:\n<sample>\n")
	b.WriteString(sample)
	b.WriteString("</sample>\n\n")
	b.WriteString("Classify this meeting as JSON:\n")
	b.WriteString("- type: one of\n")
	b.WriteString("  - \"one_on_one\": casual catch-up, 1:1 check-in, or small informal sync\n")
	b.WriteString("  - \"standup_sync\": status updates, cross-functional sync, or progress check across people/workstreams\n")
	b.WriteString("  - \"decision_planning\": kickoff, project planning, review, selection/voting, or meeting aiming at concrete decisions/assignments\n")
	b.WriteString("  - \"brainstorm\": open ideation, exploring options without final commitments\n")
	b.WriteString("  - \"interview_chat\": 1-on-1 interview, Q&A, podcast, or personal storytelling conversation (not a group selection or planning meeting)\n")
	b.WriteString("- density: \"low\" (short/light), \"medium\" (normal), or \"high\" (long/many concrete facts, numbers, decisions)\n")
	prompt := b.String()

	resp, err := m.Chat(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: "system", Content: "You classify meeting transcripts into a single JSON object. Answer with valid JSON only."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   classifyMax,
		Temperature: 0,
		Seed:        o.Seed,
		Schema:      classifySchema(),
		OnProgress:  progress,
	})
	call := Call{Step: "classify", Timings: resp.Timings, Wall: resp.Wall, Finish: resp.Finish}
	if err != nil {
		return MeetingClass{}, call, prompt, err
	}
	var mc MeetingClass
	if err := json.Unmarshal([]byte(resp.Text), &mc); err != nil {
		return MeetingClass{}, call, prompt, err
	}
	return mc, call, prompt, nil
}

// sampleTranscript picks turns from the start, middle, and end of ts up to
// roughly maxChars characters so classification stays fast on long meetings.
func sampleTranscript(ts []Turn, maxChars int) string {
	if len(ts) == 0 {
		return ""
	}
	var b strings.Builder
	writeSlice := func(label string, from, to, budget int) {
		if from >= to {
			return
		}
		if label != "" {
			fmt.Fprintf(&b, "--- %s ---\n", label)
		}
		used := 0
		for i := from; i < to; i++ {
			line := ts[i].Who + ": " + ts[i].Text + "\n"
			if used+len(line) > budget && used > 0 {
				break
			}
			if len(line) > budget {
				line = line[:budget] + "...\n"
			}
			b.WriteString(line)
			used += len(line)
		}
	}
	if len(ts) <= 12 {
		writeSlice("", 0, len(ts), maxChars)
		return b.String()
	}
	third := maxChars / 3
	mid := len(ts) / 2
	writeSlice("Start", 0, min(len(ts), 6), third)
	writeSlice("Middle", max(0, mid-3), min(len(ts), mid+3), third)
	writeSlice("End", max(0, len(ts)-6), len(ts), third)
	return b.String()
}

// styleGuidance returns a concise 2-line steering hint tailored to the
// classified meeting type and duration.
func styleGuidance(mc MeetingClass, dur time.Duration) string {
	var parts []string
	switch mc.Type {
	case "standup_sync":
		parts = append(parts, "Meeting style (sync/standup): group sections by workstream or person (e.g. \"## Anna's updates\") where participants gave separate updates; highlight blockers and concrete next steps; omit Decisions unless explicitly agreed.")
	case "one_on_one":
		parts = append(parts, "Meeting style (catch-up / 1:1): focus on key takeaways, feedback, concerns raised by each speaker, and concrete follow-ups; keep tone crisp and skip Decisions/Tasks unless explicitly agreed.")
	case "decision_planning":
		parts = append(parts, "Meeting style (planning / decision): emphasize concrete choices, trade-offs/reasons, numbers, deadlines, and clearly owned tasks.")
	case "brainstorm":
		parts = append(parts, "Meeting style (brainstorming): group by theme/option, attribute ideas and pros/cons to speakers, and clearly distinguish selected choices from open ideas (do not turn brainstormed ideas into Decisions).")
	case "interview_chat":
		parts = append(parts, "Meeting style (interview / conversation): distill the main insights, facts, and stories shared; omit Decisions and Tasks unless someone explicitly committed to follow-up work.")
	}
	mins := int(dur.Minutes())
	switch {
	case mins <= 15 || mc.Density == "low":
		parts = append(parts, "Length target: concise (2–4 focused topic sections, 2–5 short bullets each).")
	case mins <= 35 || mc.Density == "medium":
		parts = append(parts, "Length target: moderate (3–5 focused topic sections, 3–5 short bullets each).")
	default:
		parts = append(parts, "Length target: distilled (4–6 focused topic sections, 3–6 short bullets each — summarize arguments and outcomes, never retell long stories or plot recaps).")
	}
	return strings.Join(parts, "\n") + "\n"
}
