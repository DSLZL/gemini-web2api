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
	Role      string           `json:"role"`
	Content   any              `json:"content"`
	Name      string           `json:"name,omitempty"`
	ToolCalls []parsedToolCall `json:"tool_calls,omitempty"`
}

type chatRequest struct {
	Model      string        `json:"model"`
	Messages   []chatMessage `json:"messages"`
	Stream     bool          `json:"stream"`
	Tools      []toolSpec    `json:"tools"`
	ToolChoice any           `json:"tool_choice"`
}

type responseInputFunctionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Output string `json:"output"`
}

// NewHandler creates the OpenAI-compatible endpoint handler.
func NewHandler(_ any) http.Handler {
	transportClient := httpclient.New(256)
	bases := parseProxyBases(
		os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASES"),
		os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE"),
	)
	upstream := gemini.NewClient(gemini.Config{
		Client:           transportClient,
		ProxyBase:        os.Getenv("GEMINI_WEB2API_GEMINI_WEB_BASE"),
		ProxyBases:       bases,
		ResinEndpoint:    os.Getenv("RESIN_ENDPOINT"),
		ResinMode:        os.Getenv("RESIN_MODE"),
		ResinAuthVersion: os.Getenv("RESIN_AUTH_VERSION"),
		ResinProxyToken:  os.Getenv("RESIN_PROXY_TOKEN"),
		ResinPlatform:    os.Getenv("RESIN_PLATFORM"),
		ResinAccount:     os.Getenv("RESIN_ACCOUNT"),
		BL:               os.Getenv("GEMINI_WEB2API_GEMINI_BL"),
		Cookie:           "",
		SAPISID:          "",
		EnableAuth:       false,
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
		h.handleResponses(w, r)
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
	if strings.TrimSpace(req.Model) == "" {
		req.Model = models.DefaultModelName
	}

	resolved, err := models.Resolve(req.Model)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	prompt := messagesToPrompt(req.Messages, req.Tools, req.ToolChoice)
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "empty prompt")
		return
	}
	if req.Stream && len(req.Tools) == 0 {
		h.writeChatUpstreamPassthroughStream(w, r, resolved.Name, prompt, resolved.Mode, resolved.Think, &gemini.GenerateOptions{
			ExtraFields: resolved.ExtraFields,
		})
		return
	}

	result, err := h.upstream.GenerateDetailed(r.Context(), prompt, resolved.Mode, resolved.Think, &gemini.GenerateOptions{
		ExtraFields: resolved.ExtraFields,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}
	text := result.Text
	reasoning := result.ReasoningSteps

	cleanText := text
	var toolCalls []parsedToolCall
	if len(req.Tools) > 0 && !isToolChoiceNone(req.ToolChoice) {
		cleanText, toolCalls = parseToolCalls(text)
	}

	finishReason := "stop"
	message := map[string]any{
		"role":    "assistant",
		"content": cleanText,
	}
	if len(reasoning) > 0 {
		message["reasoning_content"] = reasoning[len(reasoning)-1]
	}
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
		message["tool_calls"] = toolCalls
		if strings.TrimSpace(cleanText) == "" {
			message["content"] = nil
		}
	}

	if req.Stream && (len(req.Tools) == 0 || isToolChoiceNone(req.ToolChoice)) {
		h.writeChatStream(w, resolved.Name, cleanText, reasoning)
		return
	}
	if req.Stream {
		h.writeChatToolAwareStream(w, resolved.Name, message, finishReason, reasoning)
		return
	}

	now := time.Now().Unix()
	writeJSON(w, http.StatusOK, map[string]any{
		"id":      "chatcmpl_go",
		"object":  "chat.completion",
		"created": now,
		"model":   resolved.Name,
		"choices": []map[string]any{
			{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     len(prompt) / 4,
			"completion_tokens": len(cleanText) / 4,
			"total_tokens":      (len(prompt) + len(cleanText)) / 4,
		},
	})
}

func (h *Handler) handleResponses(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	modelName, _ := req["model"].(string)
	if strings.TrimSpace(modelName) == "" {
		modelName = models.DefaultModelName
	}
	resolved, err := models.Resolve(modelName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	messages := buildResponsesMessages(req)
	tools := normalizeResponsesTools(req["tools"])
	toolChoiceValue, hasToolChoice := req["tool_choice"]
	if !hasToolChoice {
		toolChoiceValue = "auto"
	}

	prompt := messagesToPrompt(messages, tools, toolChoiceValue)
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "empty input")
		return
	}

	result, err := h.upstream.GenerateDetailed(r.Context(), prompt, resolved.Mode, resolved.Think, &gemini.GenerateOptions{
		ExtraFields: resolved.ExtraFields,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream error: "+err.Error())
		return
	}
	text := result.Text
	reasoning := result.ReasoningSteps

	cleanText := text
	var toolCalls []parsedToolCall
	if len(tools) > 0 && !isToolChoiceNone(toolChoiceValue) {
		cleanText, toolCalls = parseToolCalls(text)
	}

	rid := "resp_go"
	mid := "msg_go"
	output := make([]map[string]any, 0, len(toolCalls)+1)

	for _, tc := range toolCalls {
		output = append(output, map[string]any{
			"type":      "function_call",
			"id":        tc.ID,
			"call_id":   tc.ID,
			"name":      tc.Function.Name,
			"arguments": tc.Function.Arguments,
			"status":    "completed",
		})
	}

	if strings.TrimSpace(cleanText) != "" || len(toolCalls) == 0 {
		output = append(output, map[string]any{
			"type":   "message",
			"id":     mid,
			"role":   "assistant",
			"status": "completed",
			"content": []map[string]any{
				{
					"type":        "output_text",
					"text":        cleanText,
					"annotations": []any{},
				},
			},
		})
	}
	if len(reasoning) > 0 {
		items := make([]map[string]any, 0, len(reasoning))
		for _, step := range reasoning {
			if strings.TrimSpace(step) == "" {
				continue
			}
			items = append(items, map[string]any{
				"type": "reasoning_text",
				"text": step,
			})
		}
		if len(items) > 0 {
			output = append(output, map[string]any{
				"type":    "reasoning",
				"id":      "rs_" + rid,
				"role":    "assistant",
				"status":  "completed",
				"content": items,
			})
		}
	}

	stream, _ := req["stream"].(bool)
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)

		created := map[string]any{
			"type": "response.created",
			"response": map[string]any{
				"id":     rid,
				"object": "response",
				"status": "in_progress",
				"model":  resolved.Name,
				"output": []any{},
			},
		}
		writeSSEEvent(w, "response.created", created)
		for _, item := range output {
			itemType, _ := item["type"].(string)
			switch itemType {
			case "reasoning":
				content, _ := item["content"].([]map[string]any)
				for idx, part := range content {
					writeSSEEvent(w, "response.reasoning_text.done", map[string]any{
						"type":          "response.reasoning_text.done",
						"item_id":       item["id"],
						"content_index": idx,
						"text":          part["text"],
					})
				}
			case "function_call":
				writeSSEEvent(w, "response.function_call_arguments.done", map[string]any{
					"type":      "response.function_call_arguments.done",
					"item_id":   item["id"],
					"call_id":   item["call_id"],
					"name":      item["name"],
					"arguments": item["arguments"],
				})
			case "message":
				content, _ := item["content"].([]map[string]any)
				for idx, part := range content {
					writeSSEEvent(w, "response.output_text.done", map[string]any{
						"type":          "response.output_text.done",
						"item_id":       item["id"],
						"content_index": idx,
						"text":          part["text"],
					})
				}
			}
		}

		completed := map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     rid,
				"object": "response",
				"status": "completed",
				"model":  resolved.Name,
				"output": output,
				"usage": map[string]any{
					"input_tokens":  len(prompt) / 4,
					"output_tokens": len(cleanText) / 4,
					"total_tokens":  (len(prompt) + len(cleanText)) / 4,
				},
			},
		}
		writeSSEEvent(w, "response.completed", completed)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         rid,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "completed",
		"model":      resolved.Name,
		"output":     output,
		"usage": map[string]any{
			"input_tokens":  len(prompt) / 4,
			"output_tokens": len(cleanText) / 4,
			"total_tokens":  (len(prompt) + len(cleanText)) / 4,
		},
	})
}

func buildResponsesMessages(req map[string]any) []chatMessage {
	messages := make([]chatMessage, 0, 8)
	if instructions, _ := req["instructions"].(string); strings.TrimSpace(instructions) != "" {
		messages = append(messages, chatMessage{Role: "system", Content: instructions})
	}

	input := req["input"]
	switch v := input.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			messages = append(messages, chatMessage{Role: "user", Content: v})
		}
	case []any:
		for _, item := range v {
			switch it := item.(type) {
			case string:
				if strings.TrimSpace(it) != "" {
					messages = append(messages, chatMessage{Role: "user", Content: it})
				}
			case map[string]any:
				itemType, _ := it["type"].(string)
				role, _ := it["role"].(string)
				if itemType == "function_call_output" {
					call := responseInputFunctionCallOutput{}
					call.Type, _ = it["type"].(string)
					call.CallID, _ = it["call_id"].(string)
					call.Name, _ = it["name"].(string)
					call.Output, _ = it["output"].(string)
					messages = append(messages, chatMessage{Role: "tool", Name: call.Name, Content: call.Output})
					continue
				}
				if role == "assistant" || (itemType == "message" && role == "assistant") {
					content := it["content"]
					text, toolCalls := parseAssistantContent(content)
					msg := chatMessage{
						Role:    "assistant",
						Content: text,
					}
					if len(toolCalls) > 0 {
						msg.ToolCalls = toolCalls
					}
					messages = append(messages, msg)
					continue
				}

				content := it["content"]
				messages = append(messages, chatMessage{
					Role:    defaultString(role, "user"),
					Content: content,
				})
			}
		}
	}
	return messages
}

func parseAssistantContent(content any) (string, []parsedToolCall) {
	switch v := content.(type) {
	case string:
		return v, nil
	case []any:
		textParts := make([]string, 0, len(v))
		calls := make([]parsedToolCall, 0, 2)
		for i, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := obj["type"].(string)
			switch t {
			case "output_text":
				if txt, _ := obj["text"].(string); strings.TrimSpace(txt) != "" {
					textParts = append(textParts, txt)
				}
			case "function_call":
				name, _ := obj["name"].(string)
				arguments, _ := obj["arguments"].(string)
				callID, _ := obj["call_id"].(string)
				if strings.TrimSpace(callID) == "" {
					callID = "call_" + strconv.Itoa(i)
				}
				if strings.TrimSpace(name) == "" {
					continue
				}
				if strings.TrimSpace(arguments) == "" {
					arguments = "{}"
				}
				call := parsedToolCall{
					ID:   callID,
					Type: "function",
				}
				call.Function.Name = name
				call.Function.Arguments = arguments
				calls = append(calls, call)
			}
		}
		return strings.Join(textParts, " "), calls
	default:
		return "", nil
	}
}

func normalizeResponsesTools(raw any) []toolSpec {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]toolSpec, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}

		toolType := defaultString(asString(obj["type"]), "function")
		if strings.EqualFold(toolType, "function") {
			_, hasNested := obj["function"].(map[string]any)
			if !hasNested {
				spec := toolSpec{
					Type: "function",
					Function: &toolSpecDetail{
						Name:        strings.TrimSpace(asString(obj["name"])),
						Description: asString(obj["description"]),
					},
				}
				if params, ok := obj["parameters"].(map[string]any); ok {
					spec.Function.Parameters = params
				} else {
					spec.Function.Parameters = map[string]any{}
				}
				out = append(out, spec)
				continue
			}
		}

		spec := toolSpec{Type: toolType}
		if fnObj, ok := obj["function"].(map[string]any); ok {
			spec.Function = &toolSpecDetail{
				Name:        strings.TrimSpace(asString(fnObj["name"])),
				Description: asString(fnObj["description"]),
			}
			if params, ok := fnObj["parameters"].(map[string]any); ok {
				spec.Function.Parameters = params
			} else {
				spec.Function.Parameters = map[string]any{}
			}
		} else {
			spec.Function = &toolSpecDetail{
				Name:        strings.TrimSpace(asString(obj["name"])),
				Description: asString(obj["description"]),
				Parameters:  map[string]any{},
			}
		}
		out = append(out, spec)
	}
	return out
}

func writeSSEEvent(w http.ResponseWriter, event string, payload any) {
	raw, _ := json.Marshal(payload)
	_, _ = w.Write([]byte("event: " + event + "\n"))
	_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
}

func (h *Handler) writeChatUpstreamPassthroughStream(w http.ResponseWriter, r *http.Request, model string, prompt string, mode, think int, opts *gemini.GenerateOptions) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	streamErr := h.upstream.StreamGenerate(r.Context(), prompt, mode, think, opts, func(chunk gemini.StreamChunk) error {
		delta := map[string]any{
			"content": chunk.DeltaText,
		}
		if chunk.ChunkNumber == 1 {
			delta["role"] = "assistant"
		}
		payload := map[string]any{
			"id":      "chatcmpl_go",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         delta,
					"finish_reason": nil,
				},
			},
		}
		raw, _ := json.Marshal(payload)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return nil
	})
	if streamErr != nil {
		errPayload := map[string]any{
			"error": map[string]any{
				"message": "upstream error: " + streamErr.Error(),
				"type":    "api_error",
			},
		}
		raw, _ := json.Marshal(errPayload)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	}
	doneChunk := map[string]any{
		"id":      "chatcmpl_go",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			},
		},
	}
	raw, _ := json.Marshal(doneChunk)
	_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func streamTextChunks(text string) []string {
	const chunkSize = 8
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}
	chunks := make([]string, 0, (len(runes)+chunkSize-1)/chunkSize)
	for start := 0; start < len(runes); start += chunkSize {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func (h *Handler) writeChatStream(w http.ResponseWriter, model string, text string, reasoning []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	for _, step := range reasoning {
		if strings.TrimSpace(step) == "" {
			continue
		}
		reasoningChunk := map[string]any{
			"id":      "chatcmpl_go",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{
						"reasoning":         step,
						"reasoning_content": step,
					},
					"finish_reason": nil,
				},
			},
		}
		rawReasoning, _ := json.Marshal(reasoningChunk)
		_, _ = w.Write([]byte("data: " + string(rawReasoning) + "\n\n"))
	}

	for idx, part := range streamTextChunks(text) {
		delta := map[string]any{
			"content": part,
		}
		if idx == 0 {
			delta["role"] = "assistant"
		}
		chunk := map[string]any{
			"id":      "chatcmpl_go",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         delta,
					"finish_reason": nil,
				},
			},
		}
		raw, _ := json.Marshal(chunk)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	}
	doneChunk := map[string]any{
		"id":      "chatcmpl_go",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			},
		},
	}
	raw, _ := json.Marshal(doneChunk)
	_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (h *Handler) writeChatToolAwareStream(w http.ResponseWriter, model string, message map[string]any, finishReason string, reasoning []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	for _, step := range reasoning {
		if strings.TrimSpace(step) == "" {
			continue
		}
		reasoningChunk := map[string]any{
			"id":      "chatcmpl_go",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{
						"reasoning":         step,
						"reasoning_content": step,
					},
					"finish_reason": nil,
				},
			},
		}
		rawReasoning, _ := json.Marshal(reasoningChunk)
		_, _ = w.Write([]byte("data: " + string(rawReasoning) + "\n\n"))
	}

	if content, ok := message["content"].(string); ok && strings.TrimSpace(content) != "" {
		baseMessage := make(map[string]any, len(message))
		for key, value := range message {
			baseMessage[key] = value
		}
		delete(baseMessage, "content")
		for idx, part := range streamTextChunks(content) {
			delta := map[string]any{
				"content": part,
			}
			if idx == 0 {
				for key, value := range baseMessage {
					delta[key] = value
				}
				if _, ok := delta["role"]; !ok {
					delta["role"] = "assistant"
				}
			}
			chunk := map[string]any{
				"id":      "chatcmpl_go",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]any{
					{
						"index":         0,
						"delta":         delta,
						"finish_reason": nil,
					},
				},
			}
			raw, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
		}
	} else {
		chunk := map[string]any{
			"id":      "chatcmpl_go",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]any{
				{
					"index":         0,
					"delta":         message,
					"finish_reason": nil,
				},
			},
		}
		raw, _ := json.Marshal(chunk)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	}
	doneChunk := map[string]any{
		"id":      "chatcmpl_go",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": finishReason,
			},
		},
	}
	raw, _ := json.Marshal(doneChunk)
	_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
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

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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
