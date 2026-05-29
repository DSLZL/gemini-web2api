package openai

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gemini-web2api/internal/core/models"
	"gemini-web2api/internal/transport/httpclient"
	"gemini-web2api/internal/upstream/gemini"
)

type Handler struct {
	upstream *gemini.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

// NewHandler creates the OpenAI-compatible endpoint handler.
func NewHandler(_ any) http.Handler {
	transportClient := httpclient.New(256)
	bases := parseProxyBases(
		os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASES"),
		os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE"),
	)
	upstream := gemini.NewClient(gemini.Config{
		Client:     transportClient,
		ProxyBase:  os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE"),
		ProxyBases: bases,
		BL:         os.Getenv("GEMINI_WEB2API_GEMINI_BL"),
		Cookie:     "",
		SAPISID:    "",
		EnableAuth: false,
		Pool: gemini.PoolConfig{
			ExploreRatio:  parseEnvFloat("GEMINI_WEB2API_NODE_EXPLORE_RATIO", 0.08),
			FailThreshold: parseEnvInt("GEMINI_WEB2API_NODE_FAIL_THRESHOLD", 2),
			BanBase:       parseEnvDuration("GEMINI_WEB2API_NODE_BAN_BASE", 2*time.Minute),
			BanMax:        parseEnvDuration("GEMINI_WEB2API_NODE_BAN_MAX", 10*time.Minute),
		},
	})
	return &Handler{upstream: upstream}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		h.handleModels(w)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		h.handleChatCompletions(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
		h.handleResponses(w)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleModels(w http.ResponseWriter) {
	names := models.PublicModelNames()
	data := make([]map[string]any, 0, len(names))
	for _, name := range names {
		data = append(data, map[string]any{
			"id":     name,
			"object": "model",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func (h *Handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Model == "" {
		req.Model = "gemini-3.5-flash"
	}
	mode, think, err := models.Resolve(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	prompt := messagesToPrompt(req.Messages)
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "empty prompt")
		return
	}

	text, err := h.upstream.Generate(r.Context(), prompt, mode, think)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	if req.Stream {
		h.writeChatStream(w, req.Model, text)
		return
	}

	now := time.Now().Unix()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl_go",
		"object":  "chat.completion",
		"created": now,
		"model":   req.Model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": text,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     len(prompt) / 4,
			"completion_tokens": len(text) / 4,
			"total_tokens":      (len(prompt) + len(text)) / 4,
		},
	})
}

func (h *Handler) handleResponses(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id":     "resp_placeholder",
		"object": "response",
		"status": "completed",
		"output": []any{},
	})
}

func (h *Handler) writeChatStream(w http.ResponseWriter, model string, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	chunk := map[string]any{
		"id":      "chatcmpl_go",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"delta": map[string]any{
					"role":    "assistant",
					"content": text,
				},
				"finish_reason": "stop",
			},
		},
	}
	raw, _ := json.Marshal(chunk)
	_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func messagesToPrompt(messages []chatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		content := contentToText(msg.Content)
		switch msg.Role {
		case "system":
			if content != "" {
				parts = append(parts, "[System instruction]: "+content)
			}
		case "assistant":
			if content != "" {
				parts = append(parts, "[Assistant]: "+content)
			}
		case "tool":
			if content != "" {
				parts = append(parts, "[Tool result]: "+content)
			}
		default:
			if content != "" {
				parts = append(parts, content)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func contentToText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := obj["type"].(string)
			if t != "text" && t != "input_text" {
				continue
			}
			txt, _ := obj["text"].(string)
			if strings.TrimSpace(txt) != "" {
				parts = append(parts, txt)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		txt, _ := v["text"].(string)
		return strings.TrimSpace(txt)
	default:
		return ""
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func parseProxyBases(rawList, fallback string) []string {
	if strings.TrimSpace(rawList) == "" {
		if strings.TrimSpace(fallback) == "" {
			return nil
		}
		return []string{strings.TrimSpace(fallback)}
	}
	parts := strings.FieldsFunc(rawList, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts)+1)
	seen := make(map[string]struct{}, len(parts)+1)
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	fb := strings.TrimSpace(fallback)
	if fb != "" {
		if _, ok := seen[fb]; !ok {
			out = append(out, fb)
		}
	}
	return out
}

func parseEnvFloat(key string, def float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def
	}
	return v
}

func parseEnvInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return v
}

func parseEnvDuration(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		return def
	}
	return v
}
