package openai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
		if item.ID == "gemini-3.1-pro" {
			t.Fatalf("unexpected pro model in public list: %s", rec.Body.String())
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

func TestBasicRouteMatching(t *testing.T) {
	t.Parallel()

	h := openai.NewHandler(nil)

	t.Run("responses endpoint", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
		}
		if !strings.Contains(rec.Body.String(), `"status":"completed"`) {
			t.Fatalf("expected completed placeholder response, got: %s", rec.Body.String())
		}
	})

	t.Run("unknown path", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/unknown", nil)
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusNotFound)
		}
	})
}

func TestChatCompletionsEmptyPrompt(t *testing.T) {
	t.Parallel()

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash","messages":[]}`),
	)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "empty prompt") {
		t.Fatalf("expected empty prompt error, got: %s", rec.Body.String())
	}
}

func TestChatCompletionsStreamReturnsDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"ok from upstream"}},
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
	if !strings.Contains(body, "ok from upstream") {
		t.Fatalf("expected upstream text in SSE payload, got: %s", body)
	}
}

func TestChatCompletionsFallsBackFromEmptyNode(t *testing.T) {
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[[\"wrb.fr\",null,null]]` + "\n"))
	}))
	defer emptySrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"healthy node answer"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer okSrv.Close()

	prevBase := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	prevBases := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASES")
	prevExplore := os.Getenv("GEMINI_WEB2API_NODE_EXPLORE_RATIO")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", emptySrv.URL)
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASES", emptySrv.URL+","+okSrv.URL)
	_ = os.Setenv("GEMINI_WEB2API_NODE_EXPLORE_RATIO", "1")
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prevBase)
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASES", prevBases)
		_ = os.Setenv("GEMINI_WEB2API_NODE_EXPLORE_RATIO", prevExplore)
	})

	h := openai.NewHandler(nil)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		strings.NewReader(`{"model":"gemini-3.5-flash","messages":[{"role":"user","content":"hi"}]}`),
	)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "healthy node answer") {
		t.Fatalf("expected fallback answer from healthy node, got: %s", rec.Body.String())
	}
}

func TestChatCompletionsBansEmptyNodeThenPrefersHealthy(t *testing.T) {
	var emptyCalls int
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		emptyCalls++
		_, _ = w.Write([]byte(`[[\"wrb.fr\",null,null]]` + "\n"))
	}))
	defer emptySrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"stable answer"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer okSrv.Close()

	prevBase := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE")
	prevBases := os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASES")
	prevExplore := os.Getenv("GEMINI_WEB2API_NODE_EXPLORE_RATIO")
	prevFail := os.Getenv("GEMINI_WEB2API_NODE_FAIL_THRESHOLD")
	prevBanBase := os.Getenv("GEMINI_WEB2API_NODE_BAN_BASE")
	prevBanMax := os.Getenv("GEMINI_WEB2API_NODE_BAN_MAX")
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", emptySrv.URL)
	_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASES", emptySrv.URL+","+okSrv.URL)
	_ = os.Setenv("GEMINI_WEB2API_NODE_EXPLORE_RATIO", "1")
	_ = os.Setenv("GEMINI_WEB2API_NODE_FAIL_THRESHOLD", "1")
	_ = os.Setenv("GEMINI_WEB2API_NODE_BAN_BASE", "10m")
	_ = os.Setenv("GEMINI_WEB2API_NODE_BAN_MAX", "10m")
	t.Cleanup(func() {
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASE", prevBase)
		_ = os.Setenv("GEMINI_WEB2API_GEMINI_WEB_BASES", prevBases)
		_ = os.Setenv("GEMINI_WEB2API_NODE_EXPLORE_RATIO", prevExplore)
		_ = os.Setenv("GEMINI_WEB2API_NODE_FAIL_THRESHOLD", prevFail)
		_ = os.Setenv("GEMINI_WEB2API_NODE_BAN_BASE", prevBanBase)
		_ = os.Setenv("GEMINI_WEB2API_NODE_BAN_MAX", prevBanMax)
	})

	h := openai.NewHandler(nil)
	requestBody := `{"model":"gemini-3.5-flash","messages":[{"role":"user","content":"hi"}]}`

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody)))
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request unexpected status: got %d want %d, body=%s", rec1.Code, http.StatusOK, rec1.Body.String())
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(requestBody)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request unexpected status: got %d want %d, body=%s", rec2.Code, http.StatusOK, rec2.Body.String())
	}

	if emptyCalls != 1 {
		t.Fatalf("expected empty node to be called once then banned, got %d", emptyCalls)
	}
}
