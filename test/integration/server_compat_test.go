package integration_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gemini-web2api/internal/api/google"
	"gemini-web2api/internal/api/openai"
)

func TestServerCompat_ModelRoutesReachable(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/v1/", openai.NewHandler(nil))
	mux.Handle("/v1beta/", google.NewHandler(nil))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tests := []struct {
		name string
		path string
	}{
		{name: "openai models", path: "/v1/models"},
		{name: "google models", path: "/v1beta/models"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + tc.path)
			if err != nil {
				t.Fatalf("request %s failed: %v", tc.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200 for %s, got %d", tc.path, resp.StatusCode)
			}
		})
	}
}
