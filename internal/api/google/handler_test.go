package google_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"gemini-web2api/internal/api/google"
)

func TestModelsRoute(t *testing.T) {
	t.Parallel()

	h := google.NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
	}

	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode models response: %v", err)
	}
	if len(payload.Models) == 0 {
		t.Fatalf("expected non-empty models list, got: %+v", payload)
	}
	for _, model := range payload.Models {
		if model.Name == "models/gemini-3.1-pro" || model.Name == "models/gemini-3.1-pro-enhanced" {
			t.Fatalf("unexpected pro model in public list: %+v", payload.Models)
		}
	}
	wantVariants := []string{
		"models/gemini-3.5-flash-thinking-low",
		"models/gemini-3.5-flash-thinking-medium",
		"models/gemini-3.5-flash-thinking-high",
		"models/gemini-3.5-flash-thinking-xhigh",
		"models/gemini-3.5-flash-thinking-max",
	}
	index := make(map[string]struct{}, len(payload.Models))
	for _, model := range payload.Models {
		index[model.Name] = struct{}{}
	}
	for _, variant := range wantVariants {
		if _, ok := index[variant]; !ok {
			t.Fatalf("expected variant %s in models list, got: %+v", variant, payload.Models)
		}
	}
}

func TestGenerateContentTextResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"google answer"}},
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

	h := google.NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.5-flash:generateContent", strings.NewReader(`{
		"contents":[{"role":"user","parts":[{"text":"hi"}]}]
	}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "google answer") {
		t.Fatalf("expected response text, got: %s", rec.Body.String())
	}
}

func TestGenerateContentToolFunctionCallResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"```function_call\n{\"name\":\"lookup\",\"args\":{\"k\":\"v\"}}\n```"}},
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

	h := google.NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.5-flash:generateContent", strings.NewReader(`{
		"contents":[{"role":"user","parts":[{"text":"call tool"}]}],
		"tools":[{"functionDeclarations":[{"name":"lookup","description":"lookup","parameters":{"type":"object"}}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}}
	}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"functionCall"`) {
		t.Fatalf("expected functionCall in response parts, got: %s", rec.Body.String())
	}
}

func TestStreamGenerateContentReturnsSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"reasoning step", "stream answer"}},
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

	h := google.NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.5-flash:streamGenerateContent", strings.NewReader(`{
		"contents":[{"role":"user","parts":[{"text":"hi"}]}]
	}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got %d want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "data: ") {
		t.Fatalf("expected SSE data output, got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "\"reasoning\"") {
		t.Fatalf("expected reasoning in SSE payload, got: %s", rec.Body.String())
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	t.Parallel()

	h := google.NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1beta/unknown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusNotFound)
	}
}
