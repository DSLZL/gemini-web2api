# Repository Guidelines

## Project Structure & Module Organization

This repository is now Go-only.

- `cmd/gemini-web2api/main.go`: service entrypoint.
- `internal/api/openai`, `internal/api/google`: protocol handlers.
- `internal/core/models`: model and think-suffix parsing.
- `internal/upstream/gemini`: Gemini Web StreamGenerate client.
- `internal/proxy/resin`: Resin mode adapter.
- `internal/transport/httpclient`: high-concurrency HTTP transport defaults.
- `test/integration`: basic compatibility tests.

## Build, Test, and Development Commands

- `go run ./cmd/gemini-web2api`: start local server.
- `go test ./... -v`: run full test suite.
- `go test ./internal/api/openai -v`: iterate OpenAI handler changes quickly.
- `go test ./internal/upstream/gemini -v`: validate upstream parsing/call logic.

## Coding Style & Naming Conventions

- Follow idiomatic Go formatting (`gofmt`) and package-level cohesion.
- Keep handlers thin; move protocol-independent logic into `internal/*`.
- Preserve explicit error messages for upstream failures (do not silently return empty content).
- Model naming should stay compatible with `internal/core/models/registry.go`.

## Testing Guidelines

- Add table-driven tests for parser/mapper logic.
- For upstream behavior, prefer `httptest.Server` with deterministic payloads.
- Keep regression tests for:
  - legacy `@think` rejection
  - Pro model exclusion
  - empty prompt handling
  - upstream error surfacing

## Commit & Pull Request Guidelines

- Use Conventional Commits (`feat:`, `fix:`, `refactor:`, `docs:`).
- Include:
  - changed packages/files
  - verification commands run
  - sample request/response for API behavior changes

## Security & Configuration Tips

- Never commit real proxy credentials/tokens.
- Keep runtime values in environment variables (`.env.example`).
- Validate proxy/routing changes with a live `/v1/chat/completions` smoke request.
