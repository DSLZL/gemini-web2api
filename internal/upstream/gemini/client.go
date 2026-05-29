package gemini

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gemini-web2api/internal/proxy/resin"
)

const defaultBL = "boq_assistant-bard-web-server_20260525.09_p0"

var (
	reCodeArtifacts = regexp.MustCompile("(?s)```(?:python|javascript|text)\\?code_(?:reference|stdout)&code_event_index=\\d+\\n.*?```\\n?")
	reBardErrorCode = regexp.MustCompile(`BardErrorInfo\",\[(\d+)\]`)

	// ErrEmptyResponse marks an upstream response without usable content.
	ErrEmptyResponse = errors.New("gemini web returned empty response")
)

// Config defines Gemini web upstream settings.
type Config struct {
	Client           *http.Client
	BL               string
	ProxyBase        string
	ProxyBases       []string
	ResinEndpoint    string
	ResinMode        string
	ResinAuthVersion string
	ResinProxyToken  string
	ResinPlatform    string
	ResinAccount     string
	Cookie           string
	SAPISID          string
	EnableAuth       bool
	Pool             PoolConfig
}

// Client calls Gemini web StreamGenerate endpoint.
type Client struct {
	http      *http.Client
	bl        string
	proxyBase string
	pool      *NodePool
	resinCfg  resin.Config
	resinID   resin.Identity
	resinOn   bool
	cookie    string
	sapisid   string
	auth      bool
}

// GenerateOptions controls optional upstream payload extensions.
type GenerateOptions struct {
	ExtraFields map[int]any
}

// GenerateResult carries final answer text and optional reasoning trace.
type GenerateResult struct {
	Text           string
	ReasoningSteps []string
}

// StreamChunk is one parsed upstream text snapshot and its delta against the previous snapshot.
type StreamChunk struct {
	FullText    string
	DeltaText   string
	Reasoning   string
	RawLine     string
	ChunkNumber int
}

// NewClient creates a StreamGenerate client.
func NewClient(cfg Config) *Client {
	httpClient := cfg.Client
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	bl := strings.TrimSpace(cfg.BL)
	if bl == "" {
		bl = defaultBL
	}

	base := strings.TrimSpace(cfg.ProxyBase)
	pool := NewNodePool(cfg.ProxyBases, cfg.Pool)
	if pool == nil && base != "" {
		pool = NewNodePool([]string{base}, cfg.Pool)
	}

	resinCfg := resin.Config{
		Endpoint:    strings.TrimSpace(cfg.ResinEndpoint),
		Mode:        strings.TrimSpace(cfg.ResinMode),
		AuthVersion: strings.TrimSpace(cfg.ResinAuthVersion),
		ProxyToken:  strings.TrimSpace(cfg.ResinProxyToken),
	}
	resinID := resin.Identity{
		Platform: strings.TrimSpace(cfg.ResinPlatform),
		Account:  strings.TrimSpace(cfg.ResinAccount),
	}
	resinOn := resinCfg.Mode != ""
	if resinOn && resinCfg.Mode != "reverse" {
		proxyURL, err := proxyURLFromResin(resinCfg, resinID)
		if err != nil {
			resinOn = false
		} else {
			httpClient = cloneHTTPClientWithProxy(httpClient, proxyURL)
		}
	}

	return &Client{
		http:      httpClient,
		bl:        bl,
		proxyBase: base,
		pool:      pool,
		resinCfg:  resinCfg,
		resinID:   resinID,
		resinOn:   resinOn,
		cookie:    strings.TrimSpace(cfg.Cookie),
		sapisid:   strings.TrimSpace(cfg.SAPISID),
		auth:      cfg.EnableAuth,
	}
}

// Generate sends a StreamGenerate request and extracts final text.
func (c *Client) Generate(ctx context.Context, prompt string, mode, think int, opts *GenerateOptions) (string, error) {
	result, err := c.GenerateDetailed(ctx, prompt, mode, think, opts)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

// StreamGenerate forwards each upstream text snapshot through onChunk without waiting for full completion.
func (c *Client) StreamGenerate(ctx context.Context, prompt string, mode, think int, opts *GenerateOptions, onChunk func(StreamChunk) error) error {
	if onChunk == nil {
		return errors.New("stream callback is required")
	}
	if c.pool == nil {
		return c.streamGenerateWithCallback(ctx, prompt, mode, think, c.proxyBase, opts, onChunk)
	}

	now := time.Now()
	primary, probe := c.pool.Select(now)
	candidates := make([]string, 0, 2)
	if primary != "" {
		candidates = append(candidates, primary)
	}
	if probe != "" && probe != primary {
		candidates = append(candidates, probe)
	}
	if len(candidates) == 0 && c.proxyBase != "" {
		candidates = append(candidates, c.proxyBase)
	}
	if len(candidates) == 0 {
		return errors.New("no upstream nodes configured")
	}

	var errs []error
	for _, base := range candidates {
		err := c.streamGenerateWithCallback(ctx, prompt, mode, think, base, opts, onChunk)
		if err != nil {
			c.pool.RecordFailure(base, time.Now())
			errs = append(errs, fmt.Errorf("%s: %w", base, err))
			continue
		}
		c.pool.RecordSuccess(base, time.Now())
		return nil
	}
	return errors.Join(errs...)
}

// GenerateDetailed sends a StreamGenerate request and returns text plus reasoning trace.
func (c *Client) GenerateDetailed(ctx context.Context, prompt string, mode, think int, opts *GenerateOptions) (GenerateResult, error) {
	if c.pool == nil {
		raw, err := c.streamGenerate(ctx, prompt, mode, think, c.proxyBase, opts)
		if err != nil {
			return GenerateResult{}, err
		}
		result, err := parseStreamText(raw)
		if err != nil {
			return GenerateResult{}, err
		}
		return result, nil
	}

	now := time.Now()
	primary, probe := c.pool.Select(now)
	candidates := make([]string, 0, 2)
	if primary != "" {
		candidates = append(candidates, primary)
	}
	if probe != "" && probe != primary {
		candidates = append(candidates, probe)
	}
	if len(candidates) == 0 && c.proxyBase != "" {
		candidates = append(candidates, c.proxyBase)
	}
	if len(candidates) == 0 {
		return GenerateResult{}, errors.New("no upstream nodes configured")
	}

	var errs []error
	for _, base := range candidates {
		raw, err := c.streamGenerate(ctx, prompt, mode, think, base, opts)
		if err != nil {
			c.pool.RecordFailure(base, time.Now())
			errs = append(errs, fmt.Errorf("%s: %w", base, err))
			continue
		}
		result, parseErr := parseStreamText(raw)
		if parseErr != nil {
			if errors.Is(parseErr, ErrEmptyResponse) {
				c.pool.RecordFailure(base, time.Now())
			}
			errs = append(errs, fmt.Errorf("%s: %w", base, parseErr))
			continue
		}
		c.pool.RecordSuccess(base, time.Now())
		return result, nil
	}

	return GenerateResult{}, errors.Join(errs...)
}

func (c *Client) streamGenerate(ctx context.Context, prompt string, mode, think int, base string, opts *GenerateOptions) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("empty prompt")
	}

	inner := make([]any, 102)
	inner[0] = []any{prompt, 0, nil, nil, nil, nil, 0}
	inner[1] = []any{"en"}
	inner[2] = []any{"", "", "", nil, nil, nil, nil, nil, nil, ""}
	inner[6] = []any{0}
	inner[7] = 1
	inner[10] = 1
	inner[11] = 0
	inner[17] = []any{[]any{think}}
	inner[18] = 0
	inner[27] = 1
	inner[30] = []any{4}
	inner[41] = []any{2}
	inner[53] = 0
	inner[59] = uuidString()
	inner[61] = []any{}
	inner[68] = 1
	inner[79] = mode
	if opts != nil {
		for key, value := range opts.ExtraFields {
			if key < 0 {
				continue
			}
			if key >= len(inner) {
				extended := make([]any, key+1)
				copy(extended, inner)
				inner = extended
			}
			inner[key] = value
		}
	}

	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return "", fmt.Errorf("marshal inner payload: %w", err)
	}
	outer := []any{nil, string(innerJSON)}
	outerJSON, err := json.Marshal(outer)
	if err != nil {
		return "", fmt.Errorf("marshal outer payload: %w", err)
	}

	form := url.Values{}
	form.Set("f.req", string(outerJSON))
	reqID := time.Now().Unix() % 1000000
	target := "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	trimmedBase := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmedBase != "" {
		target = trimmedBase + "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	}
	target = target + "?bl=" + url.QueryEscape(c.bl) + "&hl=en&_reqid=" + strconv.FormatInt(reqID, 10) + "&rt=c"

	resinHeaders := make(http.Header)
	if c.resinOn {
		outURL, outHeaders, err := resin.BuildOutbound(c.resinCfg, c.resinID, target)
		if err != nil {
			return "", fmt.Errorf("build resin outbound: %w", err)
		}
		target = outURL
		resinHeaders = outHeaders
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build stream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/app")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	for key, values := range resinHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.auth && c.sapisid != "" {
		req.Header.Set("Authorization", makeSAPISIDHash(c.sapisid))
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("upstream status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func (c *Client) streamGenerateWithCallback(ctx context.Context, prompt string, mode, think int, base string, opts *GenerateOptions, onChunk func(StreamChunk) error) error {
	req, err := c.buildStreamRequest(ctx, prompt, mode, think, base, opts)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	return scanStreamChunks(resp.Body, onChunk)
}

func (c *Client) buildStreamRequest(ctx context.Context, prompt string, mode, think int, base string, opts *GenerateOptions) (*http.Request, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("empty prompt")
	}

	inner := make([]any, 102)
	inner[0] = []any{prompt, 0, nil, nil, nil, nil, 0}
	inner[1] = []any{"en"}
	inner[2] = []any{"", "", "", nil, nil, nil, nil, nil, nil, ""}
	inner[6] = []any{0}
	inner[7] = 1
	inner[10] = 1
	inner[11] = 0
	inner[17] = []any{[]any{think}}
	inner[18] = 0
	inner[27] = 1
	inner[30] = []any{4}
	inner[41] = []any{2}
	inner[53] = 0
	inner[59] = uuidString()
	inner[61] = []any{}
	inner[68] = 1
	inner[79] = mode
	if opts != nil {
		for key, value := range opts.ExtraFields {
			if key < 0 {
				continue
			}
			if key >= len(inner) {
				extended := make([]any, key+1)
				copy(extended, inner)
				inner = extended
			}
			inner[key] = value
		}
	}

	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("marshal inner payload: %w", err)
	}
	outer := []any{nil, string(innerJSON)}
	outerJSON, err := json.Marshal(outer)
	if err != nil {
		return nil, fmt.Errorf("marshal outer payload: %w", err)
	}

	form := url.Values{}
	form.Set("f.req", string(outerJSON))
	reqID := time.Now().Unix() % 1000000
	target := "https://gemini.google.com/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	trimmedBase := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmedBase != "" {
		target = trimmedBase + "/_/BardChatUi/data/assistant.lamda.BardFrontendService/StreamGenerate"
	}
	target = target + "?bl=" + url.QueryEscape(c.bl) + "&hl=en&_reqid=" + strconv.FormatInt(reqID, 10) + "&rt=c"

	resinHeaders := make(http.Header)
	if c.resinOn {
		outURL, outHeaders, err := resin.BuildOutbound(c.resinCfg, c.resinID, target)
		if err != nil {
			return nil, fmt.Errorf("build resin outbound: %w", err)
		}
		target = outURL
		resinHeaders = outHeaders
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build stream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/app")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	for key, values := range resinHeaders {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.auth && c.sapisid != "" {
		req.Header.Set("Authorization", makeSAPISIDHash(c.sapisid))
	}
	return req, nil
}

func scanStreamChunks(r io.Reader, onChunk func(StreamChunk) error) error {
	reader := bufio.NewReader(r)
	var raw strings.Builder
	lastText := ""
	chunkNo := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if line != "" {
			raw.WriteString(line)
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.Contains(trimmed, "\"wrb.fr\"") {
				texts := extractResponseTexts(trimmed)
				if len(texts) > 0 {
					full := strings.TrimSpace(texts[len(texts)-1])
					if full != "" && full != lastText {
						delta := full
						if strings.HasPrefix(full, lastText) {
							delta = strings.TrimSpace(full[len(lastText):])
						}
						if delta != "" {
							chunkNo++
							if err := onChunk(StreamChunk{
								FullText:    full,
								DeltaText:   delta,
								RawLine:     trimmed,
								ChunkNumber: chunkNo,
							}); err != nil {
								return err
							}
						}
						lastText = full
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if chunkNo == 0 {
		if code, ok := extractBardErrorCode(raw.String()); ok {
			return fmt.Errorf("gemini web upstream rejected request (BardErrorInfo %d); anonymous mode may be blocked and cookie/nonce may be required", code)
		}
		return ErrEmptyResponse
	}
	return nil
}

func parseStreamText(raw string) (GenerateResult, error) {
	texts := extractResponseTexts(raw)
	if len(texts) > 0 {
		if isIncrementalTextStream(texts) {
			return GenerateResult{
				Text: longestText(texts),
			}, nil
		}
		if final, ok := recoverFragmentedFinalText(texts); ok {
			return GenerateResult{
				Text: final,
			}, nil
		}

		finalText := strings.TrimSpace(texts[len(texts)-1])
		reasoning := make([]string, 0, len(texts)-1)
		for i := 0; i < len(texts)-1; i++ {
			if segment := strings.TrimSpace(texts[i]); segment != "" {
				reasoning = append(reasoning, segment)
			}
		}
		return GenerateResult{
			Text:           finalText,
			ReasoningSteps: reasoning,
		}, nil
	}
	if code, ok := extractBardErrorCode(raw); ok {
		return GenerateResult{}, fmt.Errorf("gemini web upstream rejected request (BardErrorInfo %d); anonymous mode may be blocked and cookie/nonce may be required", code)
	}
	return GenerateResult{}, ErrEmptyResponse
}

func extractResponseTexts(raw string) []string {
	lines := strings.Split(raw, "\n")
	texts := make([]string, 0, 8)
	for _, line := range lines {
		if !strings.Contains(line, "\"wrb.fr\"") {
			continue
		}
		var arr []any
		if err := json.Unmarshal([]byte(line), &arr); err != nil || len(arr) == 0 {
			continue
		}
		row, ok := arr[0].([]any)
		if !ok || len(row) < 3 {
			continue
		}
		innerStr, ok := row[2].(string)
		if !ok || innerStr == "" {
			continue
		}
		var inner []any
		if err := json.Unmarshal([]byte(innerStr), &inner); err != nil || len(inner) <= 4 {
			continue
		}
		parts, ok := inner[4].([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			partArr, ok := part.([]any)
			if !ok || len(partArr) <= 1 || partArr[1] == nil {
				continue
			}
			txtArr, ok := partArr[1].([]any)
			if !ok {
				continue
			}
			for _, t := range txtArr {
				if s, ok := t.(string); ok && s != "" {
					texts = append(texts, cleanGeminiText(s))
				}
			}
		}
	}
	out := make([]string, 0, len(texts))
	last := ""
	for _, text := range texts {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			continue
		}
		if trimmed == last {
			continue
		}
		out = append(out, trimmed)
		last = trimmed
	}
	return out
}

func cleanGeminiText(text string) string {
	return strings.TrimSpace(reCodeArtifacts.ReplaceAllString(text, ""))
}

func isIncrementalTextStream(texts []string) bool {
	if len(texts) <= 1 {
		return true
	}
	for i := 1; i < len(texts); i++ {
		prev := strings.TrimSpace(texts[i-1])
		curr := strings.TrimSpace(texts[i])
		if prev == "" || curr == "" {
			continue
		}
		if !strings.HasPrefix(curr, prev) {
			return false
		}
	}
	return true
}

func longestText(texts []string) string {
	longest := ""
	for _, text := range texts {
		trimmed := strings.TrimSpace(text)
		if len(trimmed) > len(longest) {
			longest = trimmed
		}
	}
	return longest
}

func recoverFragmentedFinalText(texts []string) (string, bool) {
	longest := longestText(texts)
	last := strings.TrimSpace(texts[len(texts)-1])
	if longest == "" || last == "" || longest == last {
		return "", false
	}
	for _, text := range texts {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || trimmed == longest {
			continue
		}
		if strings.HasPrefix(longest, trimmed) || strings.Contains(longest, trimmed) {
			continue
		}
		return "", false
	}
	return longest, true
}

func extractBardErrorCode(raw string) (int, bool) {
	m := reBardErrorCode.FindStringSubmatch(raw)
	if len(m) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

func makeSAPISIDHash(sapisid string) string {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha1.Sum([]byte(ts + " " + sapisid + " https://gemini.google.com"))
	return "SAPISIDHASH " + ts + "_" + hex.EncodeToString(sum[:])
}

func uuidString() string {
	now := time.Now().UnixNano()
	// lightweight unique-enough UUID-like value without extra deps
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uint32(now>>32),
		uint16(now>>16),
		uint16(now),
		uint16(now>>48),
		uint64(now)&0xFFFFFFFFFFFF,
	)
}

func proxyURLFromResin(cfg resin.Config, id resin.Identity) (*url.URL, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, errors.New("resin endpoint is required")
	}
	raw := endpoint
	if !strings.Contains(raw, "://") {
		if mode == "socks5" {
			raw = "socks5://" + raw
		} else {
			raw = "http://" + raw
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse resin endpoint: %w", err)
	}

	if mode == "forward" || mode == "connect" {
		user, err := proxyAuthUserForMode(cfg, id)
		if err != nil {
			return nil, err
		}
		u.User = url.UserPassword(user, strings.TrimSpace(cfg.ProxyToken))
		return u, nil
	}
	if mode == "socks5" {
		if strings.ToUpper(strings.TrimSpace(cfg.AuthVersion)) != "V1" {
			return nil, errors.New("socks5 requires RESIN_AUTH_VERSION=V1")
		}
		return u, nil
	}
	return u, nil
}

func proxyAuthUserForMode(cfg resin.Config, id resin.Identity) (string, error) {
	authVersion := strings.ToUpper(strings.TrimSpace(cfg.AuthVersion))
	platform := strings.TrimSpace(id.Platform)
	account := strings.TrimSpace(id.Account)
	switch authVersion {
	case "":
		fallthrough
	case "V1":
		if platform == "" && account == "" {
			return "", nil
		}
		if platform == "" {
			return account, nil
		}
		if account == "" {
			return platform, nil
		}
		return platform + "." + account, nil
	case "LEGACY_V0":
		if account != "" {
			return account, nil
		}
		if platform != "" {
			return platform, nil
		}
		return "", errors.New("legacy auth requires account or platform")
	default:
		return "", fmt.Errorf("invalid resin auth version: %s", cfg.AuthVersion)
	}
}

func cloneHTTPClientWithProxy(base *http.Client, proxyURL *url.URL) *http.Client {
	if base == nil {
		base = http.DefaultClient
	}
	cloned := *base

	var transport *http.Transport
	switch t := base.Transport.(type) {
	case *http.Transport:
		transport = t.Clone()
	default:
		if def, ok := http.DefaultTransport.(*http.Transport); ok {
			transport = def.Clone()
		}
	}
	if transport == nil {
		transport = &http.Transport{}
	}
	transport.Proxy = http.ProxyURL(proxyURL)

	authHeader := ""
	if proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		authHeader = "Basic " + token
	}
	if authHeader != "" {
		if transport.ProxyConnectHeader == nil {
			transport.ProxyConnectHeader = make(http.Header)
		}
		transport.ProxyConnectHeader.Set("Proxy-Authorization", authHeader)
	}

	cloned.Transport = transport
	return &cloned
}
