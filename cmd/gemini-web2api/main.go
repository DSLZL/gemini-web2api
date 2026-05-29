package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"gemini-web2api/internal/api/google"
	"gemini-web2api/internal/api/openai"
	"gemini-web2api/internal/observability"
)

type appDeps struct {
	Logger  *slog.Logger
	Metrics *observability.Metrics
}

func buildMux(deps appDeps) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/v1/", openai.NewHandler(deps))
	mux.Handle("/v1beta/", google.NewHandler(deps))
	return mux
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Keep WriteTimeout unset so streaming/SSE responses are not cut off.
	}
}

func main() {
	addr := os.Getenv("GEMINI_WEB2API_ADDR")
	if addr == "" {
		addr = ":8081"
	}

	deps := appDeps{
		Logger:  observability.NewLogger(),
		Metrics: observability.NewMetrics(),
	}

	server := newHTTPServer(addr, buildMux(deps))
	deps.Logger.Info("starting gemini-web2api", "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		deps.Logger.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
}
