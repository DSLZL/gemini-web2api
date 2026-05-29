package google

import (
	"encoding/json"
	"net/http"
	"strings"

	"gemini-web2api/internal/core/models"
)

type Handler struct{}

// NewHandler creates the Google native endpoint handler.
func NewHandler(_ any) http.Handler {
	return &Handler{}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1beta/models":
		h.handleModels(w)
	case r.Method == http.MethodPost &&
		(strings.HasSuffix(r.URL.Path, ":generateContent") || strings.HasSuffix(r.URL.Path, ":streamGenerateContent")):
		h.handleGenerateContent(w)
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

func (h *Handler) handleGenerateContent(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"candidates": []map[string]any{
			{
				"index": 0,
				"content": map[string]any{
					"role": "model",
					"parts": []map[string]any{
						{"text": "ok"},
					},
				},
				"finishReason": "STOP",
			},
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
