package google_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
			Name                       string   `json:"name"`
			DisplayName                string   `json:"displayName"`
			Description                string   `json:"description"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode models response: %v", err)
	}
	if len(payload.Models) == 0 {
		t.Fatalf("expected non-empty models list, got: %+v", payload)
	}
	first := payload.Models[0]
	if first.Name == "" || first.DisplayName == "" || first.Description == "" {
		t.Fatalf("expected model shape fields, got: %+v", first)
	}
	if len(first.SupportedGenerationMethods) == 0 {
		t.Fatalf("expected supportedGenerationMethods, got: %+v", first)
	}
}

func TestGenerateContentRoutes(t *testing.T) {
	t.Parallel()

	h := google.NewHandler(nil)

	t.Run("generateContent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.5-flash:generateContent", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
		}

		var payload struct {
			Candidates []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
					Role string `json:"role"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode generateContent response: %v", err)
		}
		if len(payload.Candidates) == 0 {
			t.Fatalf("expected non-empty candidates, got: %+v", payload)
		}
		c := payload.Candidates[0]
		if c.FinishReason == "" || c.Content.Role == "" || len(c.Content.Parts) == 0 {
			t.Fatalf("expected candidate shape fields, got: %+v", c)
		}
		if c.Content.Parts[0].Text == "" {
			t.Fatalf("expected candidate text field, got: %+v", c.Content.Parts[0])
		}
	})

	t.Run("streamGenerateContent", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.5-flash:streamGenerateContent", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected status: got %d want %d", rec.Code, http.StatusOK)
		}

		var payload struct {
			Candidates []struct {
				Index        int    `json:"index"`
				FinishReason string `json:"finishReason"`
				Content      struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
					Role string `json:"role"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
			t.Fatalf("decode streamGenerateContent response: %v", err)
		}
		if len(payload.Candidates) == 0 {
			t.Fatalf("expected non-empty candidates, got: %+v", payload)
		}
		c := payload.Candidates[0]
		if c.FinishReason == "" || c.Content.Role == "" || len(c.Content.Parts) == 0 {
			t.Fatalf("expected candidate shape fields, got: %+v", c)
		}
		if c.Content.Parts[0].Text == "" {
			t.Fatalf("expected candidate text field, got: %+v", c.Content.Parts[0])
		}
	})
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
