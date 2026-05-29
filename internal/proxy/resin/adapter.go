package resin

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	authVersionV1       = "V1"
	authVersionLegacyV0 = "LEGACY_V0"
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
	headers := make(http.Header)
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))

	switch mode {
	case "reverse":
		return buildReverse(cfg, id, target, headers)
	case "forward", "connect":
		auth, err := buildProxyAuthorization(cfg, id)
		if err != nil {
			return "", nil, err
		}
		headers.Set("Proxy-Authorization", "Basic "+auth)
		return target, headers, nil
	case "socks5":
		if normalizeAuthVersion(cfg.AuthVersion) != authVersionV1 {
			return "", nil, errors.New("socks5 requires RESIN_AUTH_VERSION=V1")
		}
		return target, headers, nil
	default:
		return "", nil, fmt.Errorf("invalid resin mode: %s", cfg.Mode)
	}
}

func RedactHeaders(h http.Header) {
	if h == nil {
		return
	}
	if h.Get("Proxy-Authorization") != "" {
		h.Set("Proxy-Authorization", "[REDACTED]")
	}
}

func buildReverse(cfg Config, id Identity, target string, headers http.Header) (string, http.Header, error) {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if endpoint == "" {
		return "", nil, errors.New("resin endpoint is required for reverse mode")
	}

	parsed, err := url.Parse(target)
	if err != nil {
		return "", nil, fmt.Errorf("parse target: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", nil, errors.New("target must be an absolute URL")
	}

	path := parsed.Path
	if path == "" {
		path = "/"
	}

	identity := composeIdentity(id)
	outURL := fmt.Sprintf("%s/%s/%s/%s/%s%s", endpoint, strings.TrimSpace(cfg.ProxyToken), identity, parsed.Scheme, parsed.Host, path)
	if parsed.RawQuery != "" {
		outURL += "?" + parsed.RawQuery
	}

	headers.Set("X-Resin-Account", strings.TrimSpace(id.Account))
	return outURL, headers, nil
}

func buildProxyAuthorization(cfg Config, id Identity) (string, error) {
	user, err := proxyAuthUser(normalizeAuthVersion(cfg.AuthVersion), id)
	if err != nil {
		return "", err
	}
	raw := user + ":" + strings.TrimSpace(cfg.ProxyToken)
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}

func proxyAuthUser(authVersion string, id Identity) (string, error) {
	switch authVersion {
	case authVersionV1:
		return composeIdentity(id), nil
	case authVersionLegacyV0:
		account := strings.TrimSpace(id.Account)
		platform := strings.TrimSpace(id.Platform)
		if account != "" {
			return account, nil
		}
		if platform != "" {
			return platform, nil
		}
		return "", errors.New("legacy auth requires account or platform")
	default:
		return "", fmt.Errorf("invalid resin auth version: %s", authVersion)
	}
}

func composeIdentity(id Identity) string {
	platform := strings.TrimSpace(id.Platform)
	account := strings.TrimSpace(id.Account)
	if platform == "" {
		return account
	}
	if account == "" {
		return platform
	}
	return platform + "." + account
}

func normalizeAuthVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return authVersionV1
	}
	return strings.ToUpper(v)
}
