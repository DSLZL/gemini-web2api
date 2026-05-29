# gemini-web2api

<p align="center">
  <img src="logo.png" width="200" alt="gemini-web2api logo">
</p>

[English](README.md)

基于 Go 的 Gemini Web StreamGenerate 到 OpenAI 兼容网关。

## 特性

- **OpenAI 兼容**: `/v1/chat/completions`、`/v1/models`、`/v1/responses`
- **Google 原生兼容**: `/v1beta/models`、`:generateContent`、`:streamGenerateContent`
- **匿名模型策略**: 已移除 Pro 模型与 Cookie 旧逻辑
- **思考后缀**: `-low/-medium/-high/-xhigh/-max`（不支持 `@think`）
- **Resin 集成**: 支持 `reverse|forward|connect|socks5`
- **高并发传输层**: 内置优化的 `http.Transport`

## 快速开始

```bash
go run ./cmd/gemini-web2api
```

默认监听 `:8081`（OpenAI Base URL: `http://localhost:8081/v1`）。

## 环境变量

以 `.env.example` 为模板。

- `GEMINI_WEB2API_ADDR`（默认 `:8081`）
- `HTTPS_PROXY` / `HTTP_PROXY`（可选，上游代理）
- `GEMINI_WEB2API_GEMINI_WEB_BASE`（可选，自定义 Gemini Web 基地址）
- `GEMINI_WEB2API_GEMINI_BL`（可选，BL 覆盖）
- `RESIN_ENDPOINT`、`RESIN_MODE`、`RESIN_AUTH_VERSION`、`RESIN_PLATFORM`、`RESIN_ACCOUNT`、`RESIN_PROXY_TOKEN`

## 客户端配置

### OpenAI 兼容客户端（Cherry Studio / ChatBox / SDK）

| 字段 | 值 |
|------|-----|
| Base URL | `http://localhost:8081/v1` |
| API Key | `none` |
| Model | `gemini-3.5-flash-thinking` |

### Gemini CLI

```bash
export GEMINI_API_KEY=none
export GOOGLE_GEMINI_BASE_URL=http://localhost:8081
gemini
```

## 代理示例

```bash
export HTTPS_PROXY=http://127.0.0.1:7890
go run ./cmd/gemini-web2api
```

## 开发验证

```bash
go test ./... -v
```

## License

MIT
