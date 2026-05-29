package google

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gemini-web2api/internal/core/models"
	"gemini-web2api/internal/transport/httpclient"
	"gemini-web2api/internal/upstream/gemini"
)

var modelPathRegexp = regexp.MustCompile(`/v1beta/models/([^:?]+)`)

type Handler struct {
	upstream *gemini.Client
}

type generateContentRequest struct {
	Contents          []googleContent           `json:"contents"`
	SystemInstruction googleSystemInstruction   `json:"systemInstruction"`
	Tools             []googleToolDeclGroup     `json:"tools"`
	ToolConfig        googleToolConfig          `json:"toolConfig"`
	GenerationConfig  map[string]any            `json:"generationConfig"`
	SafetySettings    []map[string]any          `json:"safetySettings"`
}

// NewHandler creates the Google native endpoint handler.
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
	case r.Method == http.MethodGet && r.URL.Path == "/v1beta/models":
		h.handleModels(w)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, ":generateContent"):
		h.handleGenerateContent(w, r, false)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, ":streamGenerateContent"):
		h.handleGenerateContent(w, r, true)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleModels(w http.ResponseWriter) {
	names := models.PublicModelNames()
	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		items = append(items, map[string]any{
			"name":        "models/" + name,
			"displayName": name,
			"description": "Gemini model proxy entry",
			"supportedGenerationMethods": []string{
				"generateContent",
				"streamGenerateContent",
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"models": items,
	})
}

func (h *Handler) handleGenerateContent(w http.ResponseWriter, r *http.Request, stream bool) {
	var req generateContentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeGoogleError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	modelName := extractModelFromPath(r.URL.Path)
	if strings.TrimSpace(modelName) == "" {
		modelName = models.DefaultModelName
	}
	resolved, err := models.Resolve(modelName)
	if err != nil {
		writeGoogleError(w, http.StatusBadRequest, err.Error())
		return
	}

	prompt := googleContentsToPrompt(req)
	if strings.TrimSpace(prompt) == "" {
		writeGoogleError(w, http.StatusBadRequest, "empty content")
		return
	}

	text, err := h.upstream.Generate(r.Context(), prompt, resolved.Mode, resolved.Think, &gemini.GenerateOptions{
		ExtraFields: resolved.ExtraFields,
	})
	if err != nil {
		writeGoogleError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}

	fcMode := strings.ToUpper(strings.TrimSpace(req.ToolConfig.FunctionCallingConfig.Mode))
	hasTools := len(req.Tools) > 0 && fcMode != "NONE"

	responseParts := make([]map[string]any, 0, 4)
	if hasTools && strings.TrimSpace(text) != "" {
		cleanText, calls := parseGoogleFunctionCalls(text)
		if len(calls) > 0 {
			if strings.TrimSpace(cleanText) != "" {
				responseParts = append(responseParts, map[string]any{"text": cleanText})
			}
			for _, call := range calls {
				responseParts = append(responseParts, map[string]any{
					"functionCall": map[string]any{
						"name": call.Name,
						"args": call.Args,
					},
				})
			}
		} else {
			responseParts = append(responseParts, map[string]any{"text": text})
		}
	} else {
		fallback := strings.TrimSpace(text)
		if fallback == "" {
			fallback = "I apologize, but I was unable to generate a response. Please try again."
		}
		responseParts = append(responseParts, map[string]any{"text": fallback})
	}

	responseObj := map[string]any{
		"candidates": []map[string]any{
			{
				"index": 0,
				"content": map[string]any{
					"role":  "model",
					"parts": responseParts,
				},
				"finishReason": "STOP",
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     len(prompt) / 4,
			"candidatesTokenCount": len(text) / 4,
			"totalTokenCount":      (len(prompt) + len(text)) / 4,
		},
		"modelVersion": resolved.Name,
	}

	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		raw, _ := json.Marshal(responseObj)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	writeJSON(w, http.StatusOK, responseObj)
}

func extractModelFromPath(path string) string {
	match := modelPathRegexp.FindStringSubmatch(path)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func writeGoogleError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"message": msg,
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
