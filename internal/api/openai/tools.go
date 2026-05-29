package openai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var toolCallPattern = regexp.MustCompile("(?s)```tool_call\\s*\\n(.*?)\\n```")

type toolSpec struct {
	Type     string          `json:"type,omitempty"`
	Function *toolSpecDetail `json:"function,omitempty"`
	Name     string          `json:"name,omitempty"`
}

type toolSpecDetail struct {
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type toolChoiceFunction struct {
	Name string `json:"name,omitempty"`
}

type toolChoice struct {
	Type     string             `json:"type,omitempty"`
	Function toolChoiceFunction `json:"function,omitempty"`
}

type parsedToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func messagesToPrompt(messages []chatMessage, tools []toolSpec, toolChoiceValue any) string {
	parts := make([]string, 0, len(messages)+1)

	if len(tools) > 0 && !isToolChoiceNone(toolChoiceValue) {
		defs := normalizeToolDefs(tools)
		if len(defs) > 0 {
			constraint := buildToolChoiceInstruction(toolChoiceValue)
			defJSON, _ := json.MarshalIndent(defs, "", "  ")
			parts = append(parts,
				"# Tool Use\n\n"+
					"You can call the following tools. Call format:\n"+
					"```tool_call\n{\"name\": \"func_name\", \"arguments\": {...}}\n```\n"+
					"When calling tools, output ONLY the tool_call block(s).\n\n"+
					"Available tools:\n"+string(defJSON)+constraint,
			)
		}
	}

	for _, msg := range messages {
		content := contentToText(msg.Content)
		switch msg.Role {
		case "system":
			if content != "" {
				parts = append(parts, "[System instruction]: "+content)
			}
		case "assistant":
			if content != "" {
				parts = append(parts, "[Assistant]: "+content)
			}
		case "tool":
			if content != "" {
				parts = append(parts, "[Tool result]: "+content)
			}
		default:
			if content != "" {
				parts = append(parts, content)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func normalizeToolDefs(tools []toolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		detail := tool.Function
		if detail == nil && strings.EqualFold(tool.Type, "function") {
			detail = &toolSpecDetail{Name: tool.Name}
		}
		if detail == nil {
			continue
		}
		name := strings.TrimSpace(detail.Name)
		if name == "" {
			continue
		}
		item := map[string]any{
			"name":        name,
			"description": detail.Description,
		}
		if len(detail.Parameters) > 0 {
			item["parameters"] = detail.Parameters
		} else {
			item["parameters"] = map[string]any{}
		}
		out = append(out, item)
	}
	return out
}

func buildToolChoiceInstruction(value any) string {
	switch v := value.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "none":
			return "\n\nIMPORTANT: Do NOT call any tools. Respond with text only."
		case "required":
			return "\n\nIMPORTANT: You MUST call at least one tool. Do not respond with text only."
		}
	case map[string]any:
		fnName := strings.TrimSpace(extractFunctionName(v))
		if fnName != "" {
			return fmt.Sprintf("\n\nIMPORTANT: You MUST call the tool %q. Do not call other tools.", fnName)
		}
	}
	return ""
}

func extractFunctionName(value map[string]any) string {
	fn, ok := value["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := fn["name"].(string)
	return name
}

func isToolChoiceNone(value any) bool {
	s, ok := value.(string)
	return ok && strings.EqualFold(strings.TrimSpace(s), "none")
}

func contentToText(content any) string {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := obj["type"].(string)
			switch t {
			case "text", "input_text":
				txt, _ := obj["text"].(string)
				if strings.TrimSpace(txt) != "" {
					parts = append(parts, txt)
				}
			case "image_url":
				imageObj, _ := obj["image_url"].(map[string]any)
				urlValue, _ := imageObj["url"].(string)
				urlValue = strings.TrimSpace(urlValue)
				if strings.HasPrefix(urlValue, "data:") {
					parts = append(parts, "\n[Image (base64)]:\n"+urlValue+"\n")
				} else if urlValue != "" {
					parts = append(parts, "\n[Image URL]:\n"+urlValue+"\n")
				}
			case "image":
				source, _ := obj["source"].(map[string]any)
				if strings.EqualFold(strings.TrimSpace(asString(source["type"])), "base64") {
					mime := strings.TrimSpace(asString(source["media_type"]))
					if mime == "" {
						mime = "image/png"
					}
					data := strings.TrimSpace(asString(source["data"]))
					if data != "" {
						parts = append(parts, fmt.Sprintf("\n[Image (base64)]:\ndata:%s;base64,%s\n", mime, data))
					}
				}
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		txt, _ := v["text"].(string)
		return strings.TrimSpace(txt)
	default:
		return ""
	}
}

func asString(value any) string {
	s, _ := value.(string)
	return s
}

func parseToolCalls(text string) (string, []parsedToolCall) {
	matches := toolCallPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return strings.TrimSpace(text), nil
	}

	var calls []parsedToolCall
	var clean strings.Builder
	last := 0

	for _, idx := range matches {
		start, end := idx[0], idx[1]
		groupStart, groupEnd := idx[2], idx[3]
		if start > last {
			clean.WriteString(text[last:start])
		}
		last = end

		block := strings.TrimSpace(text[groupStart:groupEnd])
		var payload map[string]any
		if err := json.Unmarshal([]byte(block), &payload); err != nil {
			continue
		}
		name, _ := payload["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}

		args := payload["arguments"]
		if args == nil {
			args = map[string]any{}
		}
		argJSON, _ := json.Marshal(args)

		call := parsedToolCall{
			ID:   fmt.Sprintf("call_%x", time.Now().UnixNano()),
			Type: "function",
		}
		call.Function.Name = name
		call.Function.Arguments = string(argJSON)
		calls = append(calls, call)
	}

	if last < len(text) {
		clean.WriteString(text[last:])
	}
	return strings.TrimSpace(clean.String()), calls
}
