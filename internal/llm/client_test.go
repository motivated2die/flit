package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"message":"Invalid API Key\nmore"}}`))
			return
		}
		switch r.URL.Path {
		case "/health":
			w.Write([]byte(`{"status":"ok"}`))
		case "/tokenize":
			w.Write([]byte(`{"tokens":[1,2,3]}`))
		case "/v1/chat/completions":
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"choices":[{"message":{"content":"{\"a\":1}"},"finish_reason":"stop"}],
				"timings":{"cache_n":5,"prompt_n":100,"prompt_ms":500,"predicted_n":20,"predicted_ms":1000}}`))
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k"}
	ctx := context.Background()
	if !c.Healthy(ctx) {
		t.Error("not healthy")
	}
	if n, err := c.Tokens(ctx, "abc"); err != nil || n != 3 {
		t.Errorf("tokens %d, %v", n, err)
	}
	r, err := c.Chat(ctx, Request{Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 50, Schema: json.RawMessage(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != `{"a":1}` || r.Finish != "stop" || r.Timings.PromptPerSecond() != 200 || r.Timings.PredictedPerSecond() != 20 {
		t.Errorf("response %+v", r)
	}
	if got["max_tokens"] != float64(50) || got["response_format"] == nil {
		t.Errorf("request %v", got)
	}
	if kw, _ := got["chat_template_kwargs"].(map[string]any); kw["enable_thinking"] != false {
		t.Error("thinking not switched off")
	}
	bad := &Client{BaseURL: srv.URL, Key: "wrong"}
	_, err = bad.Chat(ctx, Request{})
	if !errors.Is(err, ErrServer) || !strings.Contains(err.Error(), "Invalid API Key") || strings.Contains(err.Error(), "more") {
		t.Errorf("error %v", err)
	}
}

func TestRuntimeKept(t *testing.T) {
	for name, want := range map[string]bool{
		"llama-server.exe": true, "ggml-vulkan.dll": true, "llama-server-impl.dll": true, "LICENSE-LLVM-OpenMP": true,
		"llama-cli-impl.dll": false, "llama-cli.exe": false, "llama-bench.exe": false,
	} {
		if RuntimeKept(name) != want {
			t.Errorf("%s: %v", name, !want)
		}
	}
}
