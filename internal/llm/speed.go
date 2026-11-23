package llm

import (
	"context"
	"strings"
	"time"
)

// Speed is how fast a model reads and writes on this PC and device.
type Speed struct {
	Prompt  float64 // tokens read per second
	Predict float64 // tokens written per second
}

// speedText is the fixed text of the speed check: plain meeting talk,
// about 1,000 tokens for the candidate models.
var speedText = strings.Repeat("Anna: Let's go through the budget first. We planned twenty five thousand euros for the fair, and the stand alone costs eleven. "+
	"Ben: Then we should ask the agency for a second offer before Friday, and I can send the numbers to everyone tomorrow. ", 18)

// MeasureSpeed reads the fixed text and writes 48 tokens. The server
// should be fresh (nothing of the text cached).
func MeasureSpeed(ctx context.Context, c *Client) (Speed, error) {
	r, err := c.Chat(ctx, Request{
		Messages:  []Message{{Role: "user", Content: speedText + "\nSummarise this in one long paragraph."}},
		MaxTokens: 48, Temperature: 0.01, Seed: 1,
	})
	if err != nil {
		return Speed{}, err
	}
	return Speed{Prompt: r.Timings.PromptPerSecond(), Predict: r.Timings.PredictedPerSecond()}, nil
}

// Estimate is how long notes take for a meeting of the given length, by
// the pipeline's budget per hour of meeting (measured on the test
// meetings: docs/research/summary-pipeline.md#measured): tokens read and
// written per hour.
func (s Speed) Estimate(meeting time.Duration, readPerHour, writePerHour float64) time.Duration {
	if s.Prompt <= 0 || s.Predict <= 0 {
		return 0
	}
	h := meeting.Hours()
	return time.Duration((readPerHour*h/s.Prompt + writePerHour*h/s.Predict) * float64(time.Second))
}
