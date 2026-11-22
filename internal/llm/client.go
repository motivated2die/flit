package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to an OpenAI-compatible server: llama-server, or another
// local one. It only uses the parts llama-server documents: chat
// completions (with a JSON schema for constrained output), tokenize and
// health, and llama-server's timings when present.
type Client struct {
	BaseURL string // e.g. http://127.0.0.1:53111
	Key     string // bearer key, "" for none
	HTTP    *http.Client
}

// Message is one chat message.
type Message struct {
	Role    string `json:"role"` // system, user, assistant
	Content string `json:"content"`
}

// Request is one chat completion.
type Request struct {
	Messages    []Message
	MaxTokens   int
	Temperature float64
	// Schema constrains the answer to JSON of this schema (llama-server
	// turns it into a grammar); nil for free text.
	Schema json.RawMessage
	// Seed makes sampling repeatable (0: the server's choice).
	Seed int
	// OnProgress, when set, streams the answer and hears how far the
	// call has come: tokens of the prompt read (of its total) and tokens
	// written so far. It runs on the calling goroutine.
	OnProgress func(CallProgress)
}

// CallProgress is how far one completion has come.
type CallProgress struct {
	Read, ReadTotal int // prompt tokens read so far, of all to read (beyond the cache)
	Wrote           int // tokens written so far
}

// Timings are llama-server's figures for one completion: tokens read
// (beyond those reused from the cache) and written, and how long each took.
type Timings struct {
	CacheN      int     `json:"cache_n"`
	PromptN     int     `json:"prompt_n"`
	PromptMS    float64 `json:"prompt_ms"`
	PredictedN  int     `json:"predicted_n"`
	PredictedMS float64 `json:"predicted_ms"`
}

// PromptPerSecond and PredictedPerSecond are the speeds (0 if unknown).
func (t Timings) PromptPerSecond() float64    { return perSecond(t.PromptN, t.PromptMS) }
func (t Timings) PredictedPerSecond() float64 { return perSecond(t.PredictedN, t.PredictedMS) }

func perSecond(n int, ms float64) float64 {
	if ms <= 0 {
		return 0
	}
	return float64(n) / ms * 1000
}

// Add sums two timings.
func (t Timings) Add(o Timings) Timings {
	return Timings{t.CacheN + o.CacheN, t.PromptN + o.PromptN, t.PromptMS + o.PromptMS, t.PredictedN + o.PredictedN, t.PredictedMS + o.PredictedMS}
}

// Response is a completion's answer.
type Response struct {
	Text    string
	Finish  string // "stop", "length", …
	Timings Timings
	Wall    time.Duration // the whole request, as the client saw it
}

// ErrServer wraps an error status from the server.
var ErrServer = errors.New("llm server")

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	resp, err := c.open(ctx, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// open sends a request and returns the response when its status is a
// success; the caller closes its body.
func (c *Client) open(ctx context.Context, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		// The server's error message, not the request: it never holds
		// the prompt's text.
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		msg := resp.Status
		if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
			msg += ": " + firstLine(e.Error.Message, 200)
		}
		return nil, fmt.Errorf("%w: %s", ErrServer, msg)
	}
	return resp, nil
}

func firstLine(s string, n int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}

// Chat runs one completion. Thinking is switched off for templates that
// have it (a reasoning model's hidden tokens cost as much as visible ones).
func (c *Client) Chat(ctx context.Context, r Request) (Response, error) {
	body := map[string]any{
		"messages":             r.Messages,
		"stream":               false,
		"cache_prompt":         true,
		"chat_template_kwargs": map[string]any{"enable_thinking": false},
		"temperature":          r.Temperature,
		"top_p":                0.9,
		"min_p":                0.05,
		"presence_penalty":     0.3, // keeps small models out of loops (Yogurt's sampling.ts)
	}
	if r.MaxTokens > 0 {
		body["max_tokens"] = r.MaxTokens
	}
	if r.Seed != 0 {
		body["seed"] = r.Seed
	}
	if r.Schema != nil {
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "answer", "strict": true, "schema": r.Schema},
		}
	}
	start := time.Now()
	if r.OnProgress != nil {
		body["stream"], body["return_progress"] = true, true
		resp, err := c.stream(ctx, body, r.OnProgress)
		resp.Wall = time.Since(start)
		return resp, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Timings *Timings `json:"timings"`
		Usage   struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := c.post(ctx, "/v1/chat/completions", body, &out); err != nil {
		return Response{}, err
	}
	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("%w: no answer", ErrServer)
	}
	resp := Response{Text: out.Choices[0].Message.Content, Finish: out.Choices[0].FinishReason, Wall: time.Since(start)}
	if out.Timings != nil {
		resp.Timings = *out.Timings
	} else {
		// Another server: counts without times.
		resp.Timings = Timings{PromptN: out.Usage.PromptTokens, PredictedN: out.Usage.CompletionTokens}
	}
	return resp, nil
}

// stream reads a streamed completion (server-sent events): the text as it
// comes, llama-server's progress while it reads the prompt
// ("prompt_progress") and its timings with each token.
func (c *Client) stream(ctx context.Context, body map[string]any, progress func(CallProgress)) (Response, error) {
	resp, err := c.open(ctx, "/v1/chat/completions", body)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	var out Response
	var text strings.Builder
	var p CallProgress
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if string(data) == "[DONE]" {
			break
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Timings  *Timings `json:"timings"`
			Progress *struct {
				Total     int `json:"total"`
				Cache     int `json:"cache"`
				Processed int `json:"processed"`
			} `json:"prompt_progress"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			continue // another server's extra events
		}
		for _, ch := range ev.Choices {
			text.WriteString(ch.Delta.Content)
			if ch.FinishReason != "" {
				out.Finish = ch.FinishReason
			}
		}
		if ev.Timings != nil {
			out.Timings = *ev.Timings
			p.Wrote = ev.Timings.PredictedN
		}
		if pp := ev.Progress; pp != nil {
			p.Read, p.ReadTotal = max(0, pp.Processed-pp.Cache), max(0, pp.Total-pp.Cache)
		} else if len(ev.Choices) > 0 && p.ReadTotal > 0 {
			p.Read = p.ReadTotal // writing: the prompt is read
		}
		progress(p)
	}
	if err := sc.Err(); err != nil {
		return out, err
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	out.Text = text.String()
	if out.Finish == "" {
		return out, fmt.Errorf("%w: the answer stopped early", ErrServer)
	}
	return out, nil
}

// Tokens counts the tokens of a text with the loaded model's tokenizer.
func (c *Client) Tokens(ctx context.Context, text string) (int, error) {
	var out struct {
		Tokens []json.RawMessage `json:"tokens"`
	}
	if err := c.post(ctx, "/tokenize", map[string]any{"content": text}, &out); err != nil {
		return 0, err
	}
	return len(out.Tokens), nil
}

// Healthy reports whether the server has loaded its model and takes requests.
func (c *Client) Healthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/health", nil)
	if err != nil {
		return false
	}
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
