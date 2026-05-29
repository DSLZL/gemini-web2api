package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenerate_UsesContextCancellation(t *testing.T) {
	t.Parallel()

	var reqStarted sync.WaitGroup
	reqStarted.Add(1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqStarted.Done()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(300 * time.Millisecond):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"text":"ok"}`))
		}
	}))
	defer srv.Close()

	client := NewClient(Config{
		Client:    srv.Client(),
		ProxyBase: srv.URL,
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := client.Generate(ctx, "hello", 1, 4, nil)
		done <- err
	}()

	// Ensure request path has started before canceling, so this verifies in-flight cancellation.
	reqStarted.Wait()
	cancel()

	err := <-done
	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestGenerate_ParsesStreamGenerateLikePayload(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Fatalf("unexpected content-type: %s", got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		freq := r.Form.Get("f.req")
		if freq == "" {
			t.Fatal("expected non-empty f.req")
		}
		if _, err := url.QueryUnescape(freq); err != nil {
			// f.req itself is raw JSON string in form value; this should not fail decode path.
			t.Fatalf("unexpected f.req decode error: %v", err)
		}

		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"first chunk", "final answer"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	client := NewClient(Config{
		Client:    srv.Client(),
		ProxyBase: srv.URL,
	})
	got, err := client.Generate(context.Background(), "hello", 1, 4, nil)
	if err != nil {
		t.Fatalf("generate error: %v", err)
	}
	if got != "final answer" {
		t.Fatalf("unexpected generated text: got %q want %q", got, "final answer")
	}
}

func TestExtractResponseText_EmptyInput(t *testing.T) {
	t.Parallel()
	if got := extractResponseText(""); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}

func TestExtractBardErrorCode(t *testing.T) {
	t.Parallel()
	raw := `[["wrb.fr",null,null,null,null,[9,null,[["type.googleapis.com/assistant.boq.bard.application.BardErrorInfo",[1060]]]]]]`
	code, ok := extractBardErrorCode(raw)
	if !ok {
		t.Fatal("expected bard error code extracted")
	}
	if code != 1060 {
		t.Fatalf("unexpected bard error code: got %d want %d", code, 1060)
	}
}

func TestGenerate_PoolFallsBackOnEmptyResponse(t *testing.T) {
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[[\"wrb.fr\",null,null]]` + "\n"))
	}))
	defer emptySrv.Close()

	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"pool fallback answer"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer okSrv.Close()

	client := NewClient(Config{
		Client:     okSrv.Client(),
		ProxyBase:  emptySrv.URL,
		ProxyBases: []string{emptySrv.URL, okSrv.URL},
		Pool: PoolConfig{
			ExploreRatio:  1,
			FailThreshold: 1,
			BanBase:       5 * time.Minute,
			BanMax:        5 * time.Minute,
		},
	})

	got, err := client.Generate(context.Background(), "hello", 1, 4, nil)
	if err != nil {
		t.Fatalf("generate error: %v", err)
	}
	if got != "pool fallback answer" {
		t.Fatalf("unexpected generated text: got %q want %q", got, "pool fallback answer")
	}
}

func TestParseStreamText_EmptyResponseError(t *testing.T) {
	t.Parallel()

	_, err := parseStreamText(`[[\"wrb.fr\",null,null]]`)
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("expected ErrEmptyResponse, got: %v", err)
	}
}

func TestGenerate_AppliesExtraFieldsToPayload(t *testing.T) {
	t.Parallel()

	var capturedInner []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		freq := r.Form.Get("f.req")
		if freq == "" {
			t.Fatal("expected f.req")
		}
		var outer []any
		if err := json.Unmarshal([]byte(freq), &outer); err != nil {
			t.Fatalf("decode outer f.req: %v", err)
		}
		if len(outer) < 2 {
			t.Fatalf("unexpected outer payload shape: %#v", outer)
		}
		innerRaw, _ := outer[1].(string)
		if innerRaw == "" {
			t.Fatalf("inner payload missing: %#v", outer)
		}
		if err := json.Unmarshal([]byte(innerRaw), &capturedInner); err != nil {
			t.Fatalf("decode inner payload: %v", err)
		}

		inner := make([]any, 5)
		inner[4] = []any{
			[]any{nil, []any{"ok"}},
		}
		innerJSON, _ := json.Marshal(inner)
		line := []any{
			[]any{"wrb.fr", nil, string(innerJSON)},
		}
		lineJSON, _ := json.Marshal(line)
		_, _ = w.Write([]byte(string(lineJSON) + "\n"))
	}))
	defer srv.Close()

	client := NewClient(Config{
		Client:    srv.Client(),
		ProxyBase: srv.URL,
	})
	_, err := client.Generate(context.Background(), "hello", 3, 4, &GenerateOptions{
		ExtraFields: map[int]any{
			31: 2,
			80: 3,
		},
	})
	if err != nil {
		t.Fatalf("generate error: %v", err)
	}

	if len(capturedInner) <= 80 {
		t.Fatalf("inner payload too short: %d", len(capturedInner))
	}
	if got := capturedInner[31]; got != float64(2) {
		t.Fatalf("unexpected extra field 31: %#v", got)
	}
	if got := capturedInner[80]; got != float64(3) {
		t.Fatalf("unexpected extra field 80: %#v", got)
	}
}
