# gemini-web2api

<p align="center">
  <img src="logo.png" width="200" alt="gemini-web2api logo">
</p>

[中文文档](README_CN.md)

Go-based OpenAI-compatible gateway for Gemini Web StreamGenerate.

## Features

- **OpenAI Compatible**: `/v1/chat/completions`, `/v1/models`, `/v1/responses`
- **Google Native Compatible**: `/v1beta/models`, `:generateContent`, `:streamGenerateContent`
- **Anonymous Model Policy**: excludes Pro model and legacy cookie path
- **Thinking Depth Suffixes**: `-low/-medium/-high/-xhigh/-max` (no `@think`)
- **Resin Integration**: supports `reverse|forward|connect|socks5`
- **High-Concurrency Transport**: tuned `http.Transport` defaults

## Quick Start

```bash
go run ./cmd/gemini-web2api
```

Default listen address: `:8081` (OpenAI base URL: `http://localhost:8081/v1`)

## Environment Variables

Use `.env.example` as template.

- `GEMINI_WEB2API_ADDR` (default `:8081`)
- `HTTPS_PROXY` / `HTTP_PROXY` (optional upstream proxy)
- `GEMINI_WEB2API_GEMINI_WEB_BASE` (optional custom Gemini web base)
- `GEMINI_WEB2API_GEMINI_BL` (optional BL override)
- `RESIN_ENDPOINT`, `RESIN_MODE`, `RESIN_AUTH_VERSION`, `RESIN_PLATFORM`, `RESIN_ACCOUNT`, `RESIN_PROXY_TOKEN`

## Client Configuration

### OpenAI-compatible clients (Cherry Studio / ChatBox / SDK)

| Field | Value |
|-------|-------|
| Base URL | `http://localhost:8081/v1` |
| API Key | `none` |
| Model | `gemini-3.5-flash-thinking` |

### Gemini CLI

```bash
export GEMINI_API_KEY=none
export GOOGLE_GEMINI_BASE_URL=http://localhost:8081
gemini
```

## Proxy Example

```bash
export HTTPS_PROXY=http://127.0.0.1:7890
go run ./cmd/gemini-web2api
```

## Development

```bash
go test ./... -v
```

## License

MIT
