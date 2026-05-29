package gemini

import (
	"context"
	"crypto/sha1"
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
	Client     *http.Client
	BL         string
	ProxyBase  string
	ProxyBases []string
	Cookie     string
	SAPISID    string
	EnableAuth bool
	Pool       PoolConfig
}

// Client calls Gemini web StreamGenerate endpoint.
type Client struct {
	http      *http.Client
	bl        string
	proxyBase string
	pool      *NodePool
	cookie    string
	sapisid   string
	auth      bool
}

// GenerateOptions controls optional upstream payload extensions.
type GenerateOptions struct {
	ExtraFields map[int]any
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

	return &Client{
		http:      httpClient,
		bl:        bl,
		proxyBase: base,
		pool:      pool,
		cookie:    strings.TrimSpace(cfg.Cookie),
		sapisid:   strings.TrimSpace(cfg.SAPISID),
		auth:      cfg.EnableAuth,
	}
}

// Generate sends a StreamGenerate request and extracts final text.
func (c *Client) Generate(ctx context.Context, prompt string, mode, think int, opts *GenerateOptions) (string, error) {
	if c.pool == nil {
		raw, err := c.streamGenerate(ctx, prompt, mode, think, c.proxyBase, opts)
		if err != nil {
			return "", err
		}
		text, err := parseStreamText(raw)
		if err != nil {
			return "", err
		}
		return text, nil
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
		return "", errors.New("no upstream nodes configured")
	}

	var errs []error
	for _, base := range candidates {
		raw, err := c.streamGenerate(ctx, prompt, mode, think, base, opts)
		if err != nil {
			c.pool.RecordFailure(base, time.Now())
			errs = append(errs, fmt.Errorf("%s: %w", base, err))
			continue
		}
		text, parseErr := parseStreamText(raw)
		if parseErr != nil {
			if errors.Is(parseErr, ErrEmptyResponse) {
				c.pool.RecordFailure(base, time.Now())
			}
			errs = append(errs, fmt.Errorf("%s: %w", base, parseErr))
			continue
		}
		c.pool.RecordSuccess(base, time.Now())
		return text, nil
	}

	return "", errors.Join(errs...)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build stream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://gemini.google.com")
	req.Header.Set("Referer", "https://gemini.google.com/app")
	req.Header.Set("X-Same-Domain", "1")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
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

func parseStreamText(raw string) (string, error) {
	text := extractResponseText(raw)
	if strings.TrimSpace(text) != "" {
		return text, nil
	}
	if code, ok := extractBardErrorCode(raw); ok {
		return "", fmt.Errorf("gemini web upstream rejected request (BardErrorInfo %d); anonymous mode may be blocked and cookie/nonce may be required", code)
	}
	return "", ErrEmptyResponse
}

func extractResponseText(raw string) string {
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
					texts = append(texts, s)
				}
			}
		}
	}
	for i := len(texts) - 1; i >= 0; i-- {
		if strings.TrimSpace(texts[i]) != "" {
			return cleanGeminiText(texts[i])
		}
	}
	return ""
}

func cleanGeminiText(text string) string {
	return strings.TrimSpace(reCodeArtifacts.ReplaceAllString(text, ""))
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
