package openai_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gemini-web2api/internal/api/openai"
)

func TestModelsDoesNotContainPro(t *testing.T) {
	t.Parallel()

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, item := range body.Data {
		if item.ID == "gemini-3.1-pro" || item.ID == "gemini-3.1-pro-enhanced" {
			t.Fatalf("unexpected pro model in public list: %s", rec.Body.String())
		}
	}
	wantVariants := []string{
		"gemini-3.5-flash-thinking-low",
		"gemini-3.5-flash-thinking-medium",
		"gemini-3.5-flash-thinking-high",
		"gemini-3.5-flash-thinking-xhigh",
		"gemini-3.5-flash-thinking-max",
	}
	index := make(map[string]struct{}, len(body.Data))
	for _, item := range body.Data {
		index[item.ID] = struct{}{}
	}
	for _, variant := range wantVariants {
		if _, ok := index[variant]; !ok {
			t.Fatalf("expected variant %s in models list, got: %s", variant, rec.Body.String())
		}
	}
}

func TestChatCompletionsRejectsLegacyThink(t *testing.T) {
	t.Parallel()
	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash-thinking@think=2","messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "@think") {
		t.Fatalf("expected legacy think rejection message, got: %s", rec.Body.String())
	}
}

func TestChatCompletionsUnknownModelRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"fallback ok"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"unknown-model","messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "unknown model") {
		t.Fatalf("expected unknown model error, got: %s", rec.Body.String())
	}
}

func TestChatCompletionsParsesToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"```tool_call\n{\"name\":\"get_weather\",\"arguments\":{\"city\":\"shanghai\"}}\n```"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{
			"model":"gemini-3.5-flash",
			"messages":[{"role":"user","content":"weather?"}],
			"tools":[{"type":"function","function":{"name":"get_weather","description":"get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
			"tool_choice":"auto"
		}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"finish_reason":"tool_calls"`) {
		t.Fatalf("expected tool_calls finish reason, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"tool_calls"`) {
		t.Fatalf("expected tool_calls in response, got: %s", rec.Body.String())
	}
}

func TestResponsesReturnsFunctionCallItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"```tool_call\n{\"name\":\"search\",\"arguments\":{\"q\":\"abc\"}}\n```"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{
			"model":"gemini-3.5-flash",
			"input":"hello",
			"tools":[{"type":"function","name":"search","description":"search","parameters":{"type":"object"}}]
		}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"function_call"`) {
		t.Fatalf("expected function_call output item, got: %s", rec.Body.String())
	}
}

func TestChatCompletionsStreamReturnsDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"reasoning step", "ok from upstream"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected SSE done marker, got: %s", body)
	}
	if !strings.Contains(body, "\"content\":\"ok from upstream\"") {
		t.Fatalf("expected upstream text in SSE payload, got: %s", body)
	}
}

func TestChatCompletionsIncludesReasoningContentForCherryStudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"reasoning from upstream", "final output text"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash-thinking","messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\"reasoning_content\":\"reasoning from upstream\"") {
		t.Fatalf("expected reasoning_content in non-stream chat response, got: %s", body)
	}
	if !strings.Contains(body, "\"content\":\"final output text\"") {
		t.Fatalf("expected final content in non-stream chat response, got: %s", body)
	}
}

func TestChatCompletionsStreamUsesReasoningContentDelta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"reasoning from upstream", "final output text"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash-thinking","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\"content\":\"final output text\"") {
		t.Fatalf("expected final output text in SSE payload, got: %s", body)
	}
}

func TestChatCompletionsStreamSplitsLargeTextIntoMultipleContentChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"第一段。第二段。第三段。第四段。第五段。第六段。"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Count(body, "\"content\":\"第一段。第二段。第三段。第四段。第五段。第六段。\"") != 1 {
		t.Fatalf("expected one passthrough content chunk for one upstream chunk, got: %s", body)
	}
}

func TestChatCompletionsStreamForwardsEachUpstreamChunkImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		snapshots := []string{"你", "你好", "你好呀"}
		for _, snapshot := range snapshots {
			inner := make([]any, 5)
			inner[4] = []any{
				[]any{nil, []any{snapshot}},
			}
			innerJSON, _ := json.Marshal(inner)
			line := []any{
				[]any{"wrb.fr", nil, string(innerJSON)},
			}
			lineJSON, _ := json.Marshal(line)
			_, _ = w.Write([]byte(string(lineJSON) + "\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"你"`) ||
		!strings.Contains(body, `"content":"好"`) ||
		!strings.Contains(body, `"content":"呀"`) {
		t.Fatalf("expected one SSE content delta per upstream chunk, got: %s", body)
	}
}

func TestChatCompletionsStreamDeliversFirstChunkBeforeUpstreamCompletes(t *testing.T) {
	firstChunkWritten := make(chan struct{}, 1)
	releaseUpstream := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"你"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
		if flusher != nil {
			flusher.Flush()
		}
		firstChunkWritten <- struct{}{}
		<-releaseUpstream
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	app := httptest.NewServer(h)
	defer app.Close()

	req, err := http.NewRequest(
		http.MethodPost,
		app.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
	)
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: got %d want %d, body=%s", resp.StatusCode, http.StatusOK, string(body))
	}

	select {
	case <-firstChunkWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not write first chunk in time")
	}

	reader := bufio.NewReader(resp.Body)
	lines := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				readErr <- err
				return
			}
			if strings.Contains(line, `"content":"你"`) {
				lines <- line
				return
			}
		}
	}()

	select {
	case line := <-lines:
		if !strings.Contains(line, `"content":"你"`) {
			t.Fatalf("unexpected first stream line: %s", line)
		}
	case err := <-readErr:
		t.Fatalf("stream ended before first chunk arrived: %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("did not receive first SSE chunk before upstream completed")
	}

	close(releaseUpstream)
}

func TestResponsesStreamIncludesReasoningEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"reasoning from upstream", "final output text"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	prev := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", srv.URL)
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prev)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/responses",
		strings.NewReader(`{"model":"gemini-3.5-flash-thinking","stream":true,"input":"hi"}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "response.reasoning_text.done") {
		t.Fatalf("expected reasoning event, got: %s", body)
	}
	if !strings.Contains(body, "reasoning from upstream") {
		t.Fatalf("expected reasoning text in stream, got: %s", body)
	}
}
