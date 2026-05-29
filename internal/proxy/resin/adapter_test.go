package resin_test

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"gemini-web2api/internal/proxy/resin"
)

func decodeBasicCredentials(t *testing.T, authHeader string) (string, string) {
	t.Helper()

	if !strings.HasPrefix(authHeader, "Basic ") {
		t.Fatalf("expected basic auth header, got %q", authHeader)
	}
	encoded := strings.TrimPrefix(authHeader, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode basic auth failed: %v", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		t.Fatalf("unexpected basic auth payload: %q", string(decoded))
	}
	return parts[0], parts[1]
}

func TestBuildReverseURLAndAccountHeader(t *testing.T) {
	t.Parallel()

	cfg := resin.Config{
		Endpoint:    "http://127.0.0.1:2260",
		Mode:        "reverse",
		AuthVersion: "V1",
		ProxyToken:  "tok",
	}
	id := resin.Identity{Platform: "Nimbus", Account: "Tom"}

	outURL, headers, err := resin.BuildOutbound(cfg, id, "https://api.example.com/v1/users?id=1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantURL := "http://127.0.0.1:2260/tok/Nimbus.Tom/https/api.example.com/v1/users?id=1"
	if outURL != wantURL {
		t.Fatalf("unexpected reverse url, got %q want %q", outURL, wantURL)
	}
	if got := headers.Get("X-Resin-Account"); got != "Tom" {
		t.Fatalf("unexpected X-Resin-Account, got %q want %q", got, "Tom")
	}
}

func TestBuildForwardSetsV1ProxyAuthorization(t *testing.T) {
	t.Parallel()

	cfg := resin.Config{
		Endpoint:    "http://127.0.0.1:2260",
		Mode:        "forward",
		AuthVersion: "V1",
		ProxyToken:  "tok",
	}
	id := resin.Identity{Platform: "Nimbus", Account: "Tom"}

	outURL, headers, err := resin.BuildOutbound(cfg, id, "https://api.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outURL != "https://api.example.com" {
		t.Fatalf("unexpected outbound target, got %q", outURL)
	}
	auth := headers.Get("Proxy-Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("expected basic auth header, got %q", auth)
	}
}

func TestBuildConnectMatchesForwardWithProxyAuthorization(t *testing.T) {
	t.Parallel()

	baseCfg := resin.Config{
		Endpoint:    "http://127.0.0.1:2260",
		AuthVersion: "V1",
		ProxyToken:  "tok",
	}
	id := resin.Identity{Platform: "Nimbus", Account: "Tom"}
	target := "https://api.example.com"

	forwardCfg := baseCfg
	forwardCfg.Mode = "forward"
	connectCfg := baseCfg
	connectCfg.Mode = "connect"

	forwardURL, forwardHeaders, err := resin.BuildOutbound(forwardCfg, id, target)
	if err != nil {
		t.Fatalf("forward unexpected error: %v", err)
	}
	connectURL, connectHeaders, err := resin.BuildOutbound(connectCfg, id, target)
	if err != nil {
		t.Fatalf("connect unexpected error: %v", err)
	}

	if connectURL != forwardURL {
		t.Fatalf("unexpected outbound target, connect=%q forward=%q", connectURL, forwardURL)
	}
	forwardAuth := forwardHeaders.Get("Proxy-Authorization")
	connectAuth := connectHeaders.Get("Proxy-Authorization")
	if connectAuth != forwardAuth {
		t.Fatalf("unexpected proxy auth, connect=%q forward=%q", connectAuth, forwardAuth)
	}
	if !strings.HasPrefix(connectAuth, "Basic ") {
		t.Fatalf("expected basic auth header, got %q", connectAuth)
	}
}

func TestBuildConnectLegacyV0ProxyAuthorization(t *testing.T) {
	t.Parallel()

	cfg := resin.Config{
		Endpoint:    "http://127.0.0.1:2260",
		Mode:        "connect",
		AuthVersion: "LEGACY_V0",
		ProxyToken:  "tok",
	}
	id := resin.Identity{Platform: "Nimbus", Account: "Tom"}

	outURL, headers, err := resin.BuildOutbound(cfg, id, "https://api.example.com")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if outURL != "https://api.example.com" {
		t.Fatalf("unexpected outURL, got %q", outURL)
	}

	user, pass := decodeBasicCredentials(t, headers.Get("Proxy-Authorization"))
	if user != "Tom" {
		t.Fatalf("unexpected legacy connect user, got %q want %q", user, "Tom")
	}
	if pass != "tok" {
		t.Fatalf("unexpected legacy connect pass, got %q want %q", pass, "tok")
	}
}

func TestBuildForwardLegacyV0ProxyAuthorization(t *testing.T) {
	t.Parallel()

	cfg := resin.Config{
		Endpoint:    "http://127.0.0.1:2260",
		Mode:        "forward",
		AuthVersion: "LEGACY_V0",
		ProxyToken:  "tok",
	}
	id := resin.Identity{Platform: "Nimbus", Account: "Tom"}

	outURL, headers, err := resin.BuildOutbound(cfg, id, "https://api.example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outURL != "https://api.example.com" {
		t.Fatalf("unexpected outbound target, got %q", outURL)
	}
	user, pass := decodeBasicCredentials(t, headers.Get("Proxy-Authorization"))
	if user != "Tom" {
		t.Fatalf("unexpected basic auth user for LEGACY_V0, got %q want %q", user, "Tom")
	}
	if pass != "tok" {
		t.Fatalf("unexpected basic auth password for LEGACY_V0, got %q want %q", pass, "tok")
	}
}

func TestSocks5V1ReturnsTargetWithoutError(t *testing.T) {
	t.Parallel()

	cfg := resin.Config{
		Endpoint:    "127.0.0.1:2260",
		Mode:        "socks5",
		AuthVersion: "V1",
		ProxyToken:  "tok",
	}
	target := "https://api.example.com"

	outURL, headers, err := resin.BuildOutbound(cfg, resin.Identity{Platform: "Nimbus", Account: "Tom"}, target)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outURL != target {
		t.Fatalf("unexpected outbound target, got %q want %q", outURL, target)
	}
	if got := headers.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("unexpected proxy authorization for socks5: %q", got)
	}
}

func TestSocks5RejectsLegacyAuthVersion(t *testing.T) {
	t.Parallel()

	cfg := resin.Config{
		Endpoint:    "127.0.0.1:2260",
		Mode:        "socks5",
		AuthVersion: "LEGACY_V0",
		ProxyToken:  "tok",
	}

	_, _, err := resin.BuildOutbound(cfg, resin.Identity{}, "https://api.example.com")
	if err == nil {
		t.Fatal("expected socks5 rejection for legacy auth")
	}
	if !strings.Contains(err.Error(), "requires RESIN_AUTH_VERSION=V1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRedactHeadersMasksProxyAuthorization(t *testing.T) {
	t.Parallel()

	h := http.Header{"Proxy-Authorization": []string{"Basic abc"}}
	resin.RedactHeaders(h)

	if got := h.Get("Proxy-Authorization"); got != "[REDACTED]" {
		t.Fatalf("expected redacted header, got %q", got)
	}
}

func TestBuildOutboundRejectsInvalidMode(t *testing.T) {
	t.Parallel()

	cfg := resin.Config{
		Endpoint:    "http://127.0.0.1:2260",
		Mode:        "invalid",
		AuthVersion: "V1",
		ProxyToken:  "tok",
	}

	_, _, err := resin.BuildOutbound(cfg, resin.Identity{}, "https://api.example.com")
	if err == nil {
		t.Fatal("expected invalid mode error")
	}
	if !strings.Contains(err.Error(), "invalid resin mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}
