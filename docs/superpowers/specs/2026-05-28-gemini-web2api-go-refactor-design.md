# gemini-web2api Go 重构设计（方案二）

日期：2026-05-28  
范围：将 `gemini_web2api.py` 重构为 Go 服务，保持现有端点兼容；移除 Pro/Cookie；采用新思考等级后缀；支持 Resin 四种代理模式；以高并发长连接为核心目标。

## 1. 目标与非目标

### 1.1 目标
- 完整兼容现有端点族：
  - OpenAI：`/v1/chat/completions`、`/v1/responses`、`/v1/models`
  - Google native：`/v1beta/models`、`*:generateContent`、`*:streamGenerateContent`
- 移除 Pro 模型及 Cookie 相关配置和调用逻辑，仅保留匿名可用模型。
- 思考等级仅支持后缀：`-low`、`-medium`、`-high`、`-xhigh`、`-max`。
- 提供 Resin 四模式能力：`reverse`、`forward`、`connect`、`socks5`，按全局模式配置启用。
- 面向超高并发，验收指标以“单实例稳定 10k+ 活跃连接（以流式 SSE 为主）”为准。

### 1.2 非目标
- 本阶段不引入 Redis 到主请求链路，不以队列替代同步代理。
- 不在本阶段引入异步任务网关（方案三），仅在达不到性能门槛时升级。

## 2. 架构与模块边界

建议目录：

```text
cmd/gemini-web2api/main.go
internal/config
internal/api/openai
internal/api/google
internal/core/models
internal/upstream/gemini
internal/proxy/resin
internal/transport/httpclient
internal/observability
```

边界职责：
- `config`：配置加载、环境变量覆盖、校验、默认值。
- `api/openai` 与 `api/google`：协议层适配和响应映射，不直接做网络细节。
- `core/models`：模型白名单与思考后缀解析。
- `upstream/gemini`：上游请求构建、重试、超时、流式读取。
- `proxy/resin`：四模式统一适配器，封装 URL/鉴权/header 逻辑。
- `transport/httpclient`：`http.Transport` 并发参数与连接池策略。
- `observability`：日志、指标、健康检查。

## 3. 数据流与并发设计

主链路：
1. Handler 校验请求并识别 stream。
2. 解析模型后缀并映射内部 think 值。
3. 通过 Resin adapter 生成 outbound 策略（全局模式）。
4. 发送上游请求，执行超时/重试/取消。
5. 映射回 OpenAI 或 Google native 响应，流式场景使用 SSE 透传。

并发关键点：
- 服务端采用 Go `net/http`，为长连接配置独立超时策略。
- `http.Transport` 参数配置化：`MaxIdleConns`、`MaxIdleConnsPerHost`、`MaxConnsPerHost`、`IdleConnTimeout` 等。
- 全链路 `context.Context` 传递，客户端断开即取消上游请求。
- SSE 采用边读边写 + `Flusher`，避免整包缓存。
- 通过并发信号量保护上游，超限明确返回 429/503。

## 4. 模型与思考等级规则

### 4.1 模型约束
- 删除 Pro 模型注册项。
- 删除 `cookie_file` 配置、cookie 读取、`SAPISIDHASH` 与相关 header 注入逻辑。

### 4.2 思考等级映射
- 仅支持：`-low/-medium/-high/-xhigh/-max`
- 固定映射：
  - `-max -> 0`
  - `-xhigh -> 1`
  - `-high -> 2`
  - `-medium -> 3`
  - `-low -> 4`
- 旧写法 `@think=N` 一律返回 `400`，错误消息提示改用后缀。

## 5. Resin 代理集成设计（全局模式）

### 5.1 模式与身份
- 模式配置：`resin.mode` 取值 `reverse|forward|connect|socks5`。
- 身份来源：请求头 `X-Resin-Platform` 与 `X-Resin-Account`。
- 缺失回退：使用配置默认平台/账户（可为空，按部署策略控制）。

### 5.2 鉴权与协议
- 支持 `RESIN_AUTH_VERSION=V1|LEGACY_V0`。
- `socks5` 仅在 `V1` 下启用。
- 对 `Proxy-Authorization`、token、带凭证 URL 全量脱敏日志。

### 5.3 适配器接口（概念）
- `ResolveIdentity(req) -> {platform, account}`
- `BuildOutbound(req, mode, identity) -> {targetURL, transport, headers}`
- `ApplyHeaders(headers, identity)`

## 6. 错误处理与可观测性

错误分类：
- 参数/配置错误：400/500
- Resin 鉴权错误：401/403/407（按模式语义）
- 上游不可达/超时：502/504
- 并发保护触发：429/503

最小观测集：
- 指标：活跃连接、上游延迟分位、错误率、SSE 中断率、各代理模式请求量。
- 日志字段：`request_id`、endpoint、mode、platform/account（可掩码）、status、latency。

## 7. 测试与验收

### 7.1 单元测试
- 模型/后缀解析（含拒绝 `@think=N`）。
- Pro 拒绝策略。
- Resin 四模式构造与鉴权编码。
- 错误映射与日志脱敏。

### 7.2 协议兼容测试
- OpenAI 三端点响应形状兼容。
- Google native 三端点行为兼容。
- SSE 事件顺序与断连取消行为。

### 7.3 并发与稳定性测试
- 以流式连接为主压测，目标稳定 10k+ 活跃连接。
- 观测 goroutine、内存、P95/P99 延迟、错误率。
- 故障注入：上游超时、Resin 不可达、鉴权失败。

### 7.4 方案三升级门槛
当以下任一条件持续出现时，升级到方案三（网关+异步化）：
- 在目标连接数下 P99 或错误率持续超阈值；
- 上游抖动导致明显级联失败；
- 单实例资源利用失衡且水平扩容收益不线性。

## 8. 实施顺序（面向后续计划）
1. 搭建 Go 项目骨架与配置系统。  
2. 完成模型解析与后缀映射，移除 Pro/Cookie。  
3. 落地 OpenAI 端点兼容。  
4. 落地 Google native 端点兼容。  
5. 集成 Resin adapter（全局模式四选一）。  
6. 完成并发参数调优与压测基线。  
7. 根据压测结果判定是否升级方案三。  

