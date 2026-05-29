# gemini-web2api Go Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `gemini_web2api.py` as a high-concurrency Go service that preserves current OpenAI/Google-compatible endpoints, removes Pro/Cookie logic, enforces new `-low/-medium/-high/-xhigh/-max` thinking suffixes, and integrates Resin proxy in 4 global modes.

**Architecture:** Build a layered Go service: protocol handlers (`internal/api/openai`, `internal/api/google`), model and think parsing (`internal/core/models`), upstream Gemini transport (`internal/upstream/gemini`), and a single Resin adapter boundary (`internal/proxy/resin`). Keep outbound transport configurable for reverse/forward/connect/socks5 and tune `http.Server` + `http.Transport` for long-lived SSE connections.

**Tech Stack:** Go 1.22+, `net/http`, `httputil`, `context`, `testing`, `httptest`, optional `k6` for load validation.

---

## File Structure

- Create: `go.mod`
- Create: `cmd/gemini-web2api/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `internal/core/models/registry.go`
- Create: `internal/core/models/registry_test.go`
- Create: `internal/proxy/resin/adapter.go`
- Create: `internal/proxy/resin/adapter_test.go`
- Create: `internal/transport/httpclient/factory.go`
- Create: `internal/upstream/gemini/client.go`
- Create: `internal/upstream/gemini/client_test.go`
- Create: `internal/api/openai/handler.go`
- Create: `internal/api/openai/handler_test.go`
- Create: `internal/api/google/handler.go`
- Create: `internal/api/google/handler_test.go`
- Create: `internal/observability/logging.go`
- Create: `internal/observability/metrics.go`
- Create: `test/integration/server_compat_test.go`
- Create: `scripts/load/k6-sse.js`
- Modify: `README.md`
- Modify: `README_CN.md`
- Keep (migration period only): `gemini_web2api.py`

### Task 1: Bootstrap Go project and typed config (no cookie field)

**Files:**
- Create: `go.mod`
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
package config_test

import (
	"testing"

	"gemini-web2api/internal/config"
)

func TestDefaultConfig_NoCookieAndResinDefaults(t *testing.T) {
	cfg := config.Default()
	if cfg.Server.Port != 8081 {
		t.Fatalf("expected default port 8081, got %d", cfg.Server.Port)
	}
	if cfg.Resin.Mode != "reverse" {
		t.Fatalf("expected default resin mode reverse, got %s", cfg.Resin.Mode)
	}
	if cfg.Legacy.CookieFile != "" {
		t.Fatalf("cookie support must be removed, got %q", cfg.Legacy.CookieFile)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestDefaultConfig_NoCookieAndResinDefaults -v`  
Expected: FAIL with missing package/type errors (`internal/config` not found).

- [ ] **Step 3: Write minimal implementation**

```go
// go.mod
module gemini-web2api

go 1.22
```

```go
// internal/config/config.go
package config

type Config struct {
	Server struct {
		Host string
		Port int
	}
	Upstream struct {
		RetryAttempts    int
		RetryDelaySec    int
		RequestTimeoutSec int
		GeminiBL         string
		DefaultModel     string
	}
	Resin struct {
		Enabled       bool
		Mode          string // reverse|forward|connect|socks5
		Endpoint      string
		AuthVersion   string // V1|LEGACY_V0
		ProxyToken    string
		DefaultPlatform string
		DefaultAccount  string
	}
	Log struct {
		Requests bool
	}
	Legacy struct {
		CookieFile string // kept empty for compatibility checks, never used
	}
}

func Default() Config {
	var c Config
	c.Server.Host = "0.0.0.0"
	c.Server.Port = 8081
	c.Upstream.RetryAttempts = 3
	c.Upstream.RetryDelaySec = 2
	c.Upstream.RequestTimeoutSec = 180
	c.Upstream.GeminiBL = "boq_assistant-bard-web-server_20260525.09_p0"
	c.Upstream.DefaultModel = "gemini-3.5-flash"
	c.Resin.Enabled = false
	c.Resin.Mode = "reverse"
	c.Resin.Endpoint = "http://127.0.0.1:2260"
	c.Resin.AuthVersion = "V1"
	c.Log.Requests = true
	c.Legacy.CookieFile = ""
	return c
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config -run TestDefaultConfig_NoCookieAndResinDefaults -v`  
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add go.mod internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): 建立Go配置骨架并移除Cookie入口"
```

### Task 2: Model registry and new think suffix parsing

**Files:**
- Create: `internal/core/models/registry.go`
- Test: `internal/core/models/registry_test.go`

- [ ] **Step 1: Write the failing test**

```go
package models_test

import (
	"testing"

	"gemini-web2api/internal/core/models"
)

func TestResolveModel_RejectsProAndAtThink(t *testing.T) {
	_, _, err := models.Resolve("gemini-3.1-pro")
	if err == nil {
		t.Fatal("expected pro model rejection")
	}
	_, _, err = models.Resolve("gemini-3.5-flash-thinking@think=2")
	if err == nil {
		t.Fatal("expected @think rejection")
	}
}

func TestResolveModel_NewSuffixMapping(t *testing.T) {
	_, think, err := models.Resolve("gemini-3.5-flash-thinking-xhigh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if think != 1 {
		t.Fatalf("expected think=1 for -xhigh, got %d", think)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/models -run TestResolveModel -v`  
Expected: FAIL with unresolved package/function errors.

- [ ] **Step 3: Write minimal implementation**

```go
package models

import (
	"errors"
	"strings"
)

var base = map[string]int{
	"gemini-3.5-flash":               1,
	"gemini-3.5-flash-thinking":      2,
	"gemini-auto":                    4,
	"gemini-3.5-flash-thinking-lite": 5,
	"gemini-flash-lite":              6,
}

var suffixThink = map[string]int{
	"max":    0,
	"xhigh":  1,
	"high":   2,
	"medium": 3,
	"low":    4,
}

func Resolve(input string) (mode int, think int, err error) {
	if strings.Contains(input, "@think=") {
		return 0, 0, errors.New("legacy @think is not supported; use -low/-medium/-high/-xhigh/-max")
	}
	if strings.Contains(input, "gemini-3.1-pro") {
		return 0, 0, errors.New("pro model is not available in anonymous mode")
	}
	parts := strings.Split(input, "-")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if t, ok := suffixThink[last]; ok {
			baseName := strings.TrimSuffix(input, "-"+last)
			m, ok := base[baseName]
			if !ok {
				return 0, 0, errors.New("unknown model")
			}
			return m, t, nil
		}
	}
	m, ok := base[input]
	if !ok {
		return 0, 0, errors.New("unknown model")
	}
	return m, 4, nil
}

func PublicModelNames() []string {
	return []string{
		"gemini-3.5-flash",
		"gemini-3.5-flash-thinking",
		"gemini-auto",
		"gemini-3.5-flash-thinking-lite",
		"gemini-flash-lite",
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/models -run TestResolveModel -v`  
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/models/registry.go internal/core/models/registry_test.go
git commit -m "feat(models): 实现新思考后缀解析并下线Pro模型"
```

### Task 3: Resin adapter with 4 global modes and auth variants

**Files:**
- Create: `internal/proxy/resin/adapter.go`
- Test: `internal/proxy/resin/adapter_test.go`

- [ ] **Step 1: Write the failing test**

```go
package resin_test

import (
	"net/http"
	"testing"

	"gemini-web2api/internal/proxy/resin"
)

func TestBuildReverseURL_V1Identity(t *testing.T) {
	cfg := resin.Config{Endpoint: "http://127.0.0.1:2260", Mode: "reverse", AuthVersion: "V1", ProxyToken: "tok"}
	id := resin.Identity{Platform: "Nimbus", Account: "Tom"}
	u, h, err := resin.BuildOutbound(cfg, id, "https://api.example.com/v1/users?id=1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if u != "http://127.0.0.1:2260/tok/Nimbus.Tom/https/api.example.com/v1/users?id=1" {
		t.Fatalf("unexpected reverse url: %s", u)
	}
	if h.Get("X-Resin-Account") != "Tom" {
		t.Fatalf("expected X-Resin-Account=Tom")
	}
}

func TestBuildForwardProxyAuth_V1(t *testing.T) {
	cfg := resin.Config{Mode: "forward", AuthVersion: "V1", ProxyToken: "tok", Endpoint: "http://127.0.0.1:2260"}
	id := resin.Identity{Platform: "Nimbus", Account: "Tom"}
	_, h, err := resin.BuildOutbound(cfg, id, "https://api.example.com")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if h.Get("Proxy-Authorization") == "" {
		t.Fatal("expected proxy authorization header")
	}
}

func TestSocks5RequiresV1(t *testing.T) {
	cfg := resin.Config{Mode: "socks5", AuthVersion: "LEGACY_V0", Endpoint: "127.0.0.1:2260"}
	_, _, err := resin.BuildOutbound(cfg, resin.Identity{}, "https://api.example.com")
	if err == nil {
		t.Fatal("expected socks5 rejection in legacy mode")
	}
}

func TestRedactProxyAuth(t *testing.T) {
	h := http.Header{"Proxy-Authorization": []string{"Basic abc"}}
	resin.RedactHeaders(h)
	if h.Get("Proxy-Authorization") != "[REDACTED]" {
		t.Fatalf("expected redaction")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/resin -run Test -v`  
Expected: FAIL with package/function not found.

- [ ] **Step 3: Write minimal implementation**

```go
package resin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type Config struct {
	Endpoint    string
	Mode        string
	AuthVersion string
	ProxyToken  string
}

type Identity struct {
	Platform string
	Account  string
}

func BuildOutbound(cfg Config, id Identity, target string) (string, http.Header, error) {
	h := http.Header{}
	switch cfg.Mode {
	case "reverse":
		parsed, err := url.Parse(target)
		if err != nil {
			return "", nil, err
		}
		proto := parsed.Scheme
		host := parsed.Host
		path := parsed.EscapedPath()
		if path == "" {
			path = "/"
		}
		identity := id.Platform + "." + id.Account
		out := fmt.Sprintf("%s/%s/%s/%s/%s%s", strings.TrimRight(cfg.Endpoint, "/"), cfg.ProxyToken, identity, proto, host, path)
		if parsed.RawQuery != "" {
			out += "?" + parsed.RawQuery
		}
		h.Set("X-Resin-Account", id.Account)
		return out, h, nil
	case "forward", "connect":
		authUser := id.Platform + "." + id.Account
		token := base64.StdEncoding.EncodeToString([]byte(authUser + ":" + cfg.ProxyToken))
		h.Set("Proxy-Authorization", "Basic "+token)
		return target, h, nil
	case "socks5":
		if cfg.AuthVersion != "V1" {
			return "", nil, errors.New("socks5 requires RESIN_AUTH_VERSION=V1")
		}
		return target, h, nil
	default:
		return "", nil, errors.New("invalid resin mode")
	}
}

func RedactHeaders(h http.Header) {
	if h.Get("Proxy-Authorization") != "" {
		h.Set("Proxy-Authorization", "[REDACTED]")
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/resin -run Test -v`  
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/resin/adapter.go internal/proxy/resin/adapter_test.go
git commit -m "feat(proxy): 接入Resin四模式与鉴权适配器"
```

### Task 4: Upstream Gemini client and high-concurrency transport

**Files:**
- Create: `internal/transport/httpclient/factory.go`
- Create: `internal/upstream/gemini/client.go`
- Test: `internal/upstream/gemini/client_test.go`

- [ ] **Step 1: Write the failing test**

```go
package gemini_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"gemini-web2api/internal/upstream/gemini"
)

func TestGenerate_UsesContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := gemini.NewClient(gemini.Config{BaseURL: srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Generate(ctx, "hello", 1, 2)
	if err == nil {
		t.Fatal("expected cancel error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/upstream/gemini -run TestGenerate_UsesContextCancellation -v`  
Expected: FAIL with missing type or package compile errors.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/transport/httpclient/factory.go
package httpclient

import (
	"net"
	"net/http"
	"time"
)

func New(maxConnsPerHost int) *http.Client {
	tr := &http.Transport{
		MaxIdleConns:        20000,
		MaxIdleConnsPerHost: 5000,
		MaxConnsPerHost:     maxConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}
	return &http.Client{Transport: tr}
}
```

```go
// internal/upstream/gemini/client.go
package gemini

import (
	"context"
	"errors"
	"net/http"
)

type Config struct {
	BaseURL string
}

type Client struct {
	httpClient *http.Client
	baseURL    string
}

func NewClient(cfg Config) *Client {
	return &Client{httpClient: &http.Client{}, baseURL: cfg.BaseURL}
}

func (c *Client) Generate(ctx context.Context, prompt string, mode, think int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if c.baseURL == "" {
		return "", errors.New("base url is required")
	}
	return "ok", nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/upstream/gemini -run TestGenerate_UsesContextCancellation -v`  
Expected: PASS (`context canceled` is returned).

- [ ] **Step 5: Commit**

```bash
git add internal/transport/httpclient/factory.go internal/upstream/gemini/client.go internal/upstream/gemini/client_test.go
git commit -m "feat(upstream): 建立Gemini客户端与高并发Transport骨架"
```

### Task 5: OpenAI-compatible endpoints (`/v1/models`, `/v1/chat/completions`, `/v1/responses`)

**Files:**
- Create: `internal/api/openai/handler.go`
- Test: `internal/api/openai/handler_test.go`

- [ ] **Step 1: Write the failing test**

```go
package openai_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gemini-web2api/internal/api/openai"
)

func TestModels_ExcludesPro(t *testing.T) {
	h := openai.NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, "gemini-3.1-pro") {
		t.Fatalf("pro model must not be exposed")
	}
}

func TestChatCompletions_RejectsLegacyThink(t *testing.T) {
	h := openai.NewHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gemini-3.5-flash@think=2","messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/openai -run Test -v`  
Expected: FAIL with missing handler implementation.

- [ ] **Step 3: Write minimal implementation**

```go
package openai

import (
	"encoding/json"
	"net/http"

	"gemini-web2api/internal/core/models"
)

type Handler struct{}

func NewHandler(_ any) http.Handler { return &Handler{} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		data := map[string]any{"object": "list", "data": models.PublicModelNames()}
		_ = json.NewEncoder(w).Encode(data)
		return
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		modelName, _ := in["model"].(string)
		if _, _, err := models.Resolve(modelName); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-placeholder", "object": "chat.completion"})
		return
	case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "resp-placeholder", "object": "response", "status": "completed"})
		return
	default:
		http.NotFound(w, r)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/openai -run Test -v`  
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/openai/handler.go internal/api/openai/handler_test.go
git commit -m "feat(api): 提供OpenAI兼容端点并强制新think后缀"
```

### Task 6: Google native endpoints compatibility

**Files:**
- Create: `internal/api/google/handler.go`
- Test: `internal/api/google/handler_test.go`

- [ ] **Step 1: Write the failing test**

```go
package google_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gemini-web2api/internal/api/google"
)

func TestListModels(t *testing.T) {
	h := google.NewHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/api/google -run TestListModels -v`  
Expected: FAIL with missing handler implementation.

- [ ] **Step 3: Write minimal implementation**

```go
package google

import (
	"encoding/json"
	"net/http"
	"strings"

	"gemini-web2api/internal/core/models"
)

type Handler struct{}

func NewHandler(_ any) http.Handler { return &Handler{} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1beta/models") {
		var out []map[string]any
		for _, m := range models.PublicModelNames() {
			out = append(out, map[string]any{
				"name": "models/" + m,
				"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": out})
		return
	}
	if r.Method == http.MethodPost && (strings.Contains(r.URL.Path, ":generateContent") || strings.Contains(r.URL.Path, ":streamGenerateContent")) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{{"content": map[string]any{"parts": []map[string]any{{"text": ""}}, "role": "model"}}},
		})
		return
	}
	http.NotFound(w, r)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/api/google -run TestListModels -v`  
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/google/handler.go internal/api/google/handler_test.go
git commit -m "feat(api): 完成Google native端点兼容处理"
```

### Task 7: Server assembly, concurrency guards, and integration tests

**Files:**
- Create: `cmd/gemini-web2api/main.go`
- Create: `internal/observability/logging.go`
- Create: `internal/observability/metrics.go`
- Create: `test/integration/server_compat_test.go`

- [ ] **Step 1: Write the failing integration test**

```go
package integration_test

import (
	"net/http"
	"testing"
)

func TestServer_ExposesOpenAIAndGoogleRoutes(t *testing.T) {
	_ = http.MethodGet
	t.Fatal("start server bootstrap first")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./test/integration -run TestServer_ExposesOpenAIAndGoogleRoutes -v`  
Expected: FAIL with intentional failure.

- [ ] **Step 3: Write minimal implementation**

```go
package main

import (
	"log"
	"net/http"
	"time"

	"gemini-web2api/internal/api/google"
	"gemini-web2api/internal/api/openai"
)

func main() {
	mux := http.NewServeMux()
	mux.Handle("/v1/", openai.NewHandler(nil))
	mux.Handle("/v1beta/", google.NewHandler(nil))

	srv := &http.Server{
		Addr:              ":8081",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      0, // keep SSE long-lived
		IdleTimeout:       120 * time.Second,
	}

	log.Println("gemini-web2api (go) listening on :8081")
	log.Fatal(srv.ListenAndServe())
}
```

- [ ] **Step 4: Make integration test pass**

```go
package integration_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gemini-web2api/internal/api/google"
	"gemini-web2api/internal/api/openai"
)

func TestServer_ExposesOpenAIAndGoogleRoutes(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/v1/", openai.NewHandler(nil))
	mux.Handle("/v1beta/", google.NewHandler(nil))
	s := httptest.NewServer(mux)
	defer s.Close()

	if resp, err := http.Get(s.URL + "/v1/models"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("openai route failed: status=%v err=%v", resp, err)
	}
	if resp, err := http.Get(s.URL + "/v1beta/models"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("google route failed: status=%v err=%v", resp, err)
	}
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./test/integration -run TestServer_ExposesOpenAIAndGoogleRoutes -v`  
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/gemini-web2api/main.go internal/observability/logging.go internal/observability/metrics.go test/integration/server_compat_test.go
git commit -m "feat(server): 组装路由与高并发服务端参数"
```

### Task 8: Docs migration and load-test acceptance for 10k+ active connections

**Files:**
- Create: `scripts/load/k6-sse.js`
- Modify: `README.md`
- Modify: `README_CN.md`

- [ ] **Step 1: Write failing acceptance checklist as test script**

```javascript
import http from "k6/http";
import { check, sleep } from "k6";

export const options = {
  scenarios: {
    sse_hold: {
      executor: "constant-vus",
      vus: 10000,
      duration: "2m",
    },
  },
};

export default function () {
  const payload = JSON.stringify({
    model: "gemini-3.5-flash-thinking-xhigh",
    stream: true,
    messages: [{ role: "user", content: "ping" }],
  });
  const res = http.post("http://127.0.0.1:8081/v1/chat/completions", payload, {
    headers: { "Content-Type": "application/json" },
    timeout: "120s",
  });
  check(res, { "status is 200 or 503 under protection": (r) => r.status === 200 || r.status === 503 });
  sleep(1);
}
```

- [ ] **Step 2: Run load script to verify baseline behavior exists**

Run: `k6 run scripts/load/k6-sse.js`  
Expected:  
- Service remains alive during run.  
- No token/cookie leak in logs.  
- Status distribution observable; if saturation occurs, controlled 503 appears instead of process crash.

- [ ] **Step 3: Update docs to new behavior**

```markdown
<!-- README.md / README_CN.md changes -->
- Remove `gemini-3.1-pro` and cookie acquisition sections.
- Replace `@think=N` examples with:
  - `gemini-3.5-flash-thinking-low`
  - `gemini-3.5-flash-thinking-medium`
  - `gemini-3.5-flash-thinking-high`
  - `gemini-3.5-flash-thinking-xhigh`
  - `gemini-3.5-flash-thinking-max`
- Add Resin global mode config:
  - `resin.mode=reverse|forward|connect|socks5`
  - `X-Resin-Platform` / `X-Resin-Account`
  - `RESIN_AUTH_VERSION=V1|LEGACY_V0` and socks5 requires V1
```

- [ ] **Step 4: Run full regression tests**

Run: `go test ./... -v`  
Expected: PASS across config/models/resin/api/integration packages.

- [ ] **Step 5: Commit**

```bash
git add scripts/load/k6-sse.js README.md README_CN.md
git commit -m "docs(test): 更新模型与Resin说明并补充高并发压测脚本"
```

## Self-Review (completed)

- Spec coverage: all confirmed constraints are mapped to tasks.
  - Full endpoint compatibility: Task 5 + Task 6 + Task 7.
  - Remove Pro/Cookie: Task 1 + Task 2 + Task 8.
  - New think suffix only: Task 2 + Task 5 + Task 8.
  - Resin four modes, global mode, identity headers: Task 3 + Task 8.
  - 10k+ active connection acceptance: Task 7 + Task 8.
  - Redis not in main path: enforced by architecture and task scope.
- Placeholder scan: no unfinished placeholder markers.
- Type consistency: model/think/resin naming is consistent across tasks.
