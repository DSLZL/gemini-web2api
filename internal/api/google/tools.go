package google

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	functionCallBlockPattern = regexp.MustCompile("(?s)```function_call\\s*\\n(.*?)\\n```")
	functionCallInlinePattern = regexp.MustCompile("(?s)(?:^|\\n)function_call\\s*\\n(\\{.*?\\})(?:\\n|$)")
)

type googleToolConfig struct {
	FunctionCallingConfig struct {
		Mode                 string   `json:"mode"`
		AllowedFunctionNames []string `json:"allowedFunctionNames"`
	} `json:"functionCallingConfig"`
}

type googleToolDeclGroup struct {
	FunctionDeclarations []googleFunctionDecl `json:"functionDeclarations"`
}

type googleFunctionDecl struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description"`
	Parameters           map[string]any `json:"parameters"`
	ParametersJSONSchema map[string]any `json:"parametersJsonSchema"`
}

type googleSystemInstruction struct {
	Parts []googlePart `json:"parts"`
}

type googleContent struct {
	Role  string      `json:"role"`
	Parts []googlePart `json:"parts"`
}

type googlePart struct {
	Text             string               `json:"text"`
	InlineData       *googleInlineData    `json:"inlineData"`
	FunctionCall     *googleFunctionCall  `json:"functionCall"`
	FunctionResponse *googleFunctionReply `json:"functionResponse"`
}

type googleInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type googleFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type googleFunctionReply struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type parsedGoogleFunctionCall struct {
	Name string
	Args map[string]any
}

func googleContentsToPrompt(req generateContentRequest) string {
	parts := make([]string, 0, 8)
	toolDefs := collectGoogleToolDefs(req.Tools)
	fcMode := strings.ToUpper(strings.TrimSpace(req.ToolConfig.FunctionCallingConfig.Mode))

	if sys := buildSystemInstruction(req.SystemInstruction); sys != "" {
		if len(toolDefs) > 0 && fcMode != "NONE" {
			parts = append(parts, sys+"\n\n"+buildGoogleToolPrompt(toolDefs)+buildGoogleToolChoiceInstruction(req.ToolConfig))
		} else {
			parts = append(parts, sys)
		}
	} else if len(toolDefs) > 0 && fcMode != "NONE" {
		parts = append(parts, buildGoogleToolPrompt(toolDefs)+buildGoogleToolChoiceInstruction(req.ToolConfig))
	}

	for _, content := range req.Contents {
		role := strings.ToLower(strings.TrimSpace(content.Role))
		msgParts := make([]string, 0, len(content.Parts))
		for _, part := range content.Parts {
			switch {
			case strings.TrimSpace(part.Text) != "":
				msgParts = append(msgParts, part.Text)
			case part.InlineData != nil && strings.TrimSpace(part.InlineData.Data) != "":
				mime := strings.TrimSpace(part.InlineData.MimeType)
				if mime == "" {
					mime = "image/png"
				}
				msgParts = append(msgParts, fmt.Sprintf("[Image (base64)]: data:%s;base64,%s", mime, part.InlineData.Data))
			case part.FunctionCall != nil && strings.TrimSpace(part.FunctionCall.Name) != "":
				payload := map[string]any{
					"name": part.FunctionCall.Name,
					"args": part.FunctionCall.Args,
				}
				raw, _ := json.Marshal(payload)
				msgParts = append(msgParts, "```function_call\n"+string(raw)+"\n```")
			case part.FunctionResponse != nil:
				payload, _ := json.Marshal(part.FunctionResponse.Response)
				msgParts = append(msgParts, fmt.Sprintf("[Tool result for %s]: %s", part.FunctionResponse.Name, string(payload)))
			}
		}
		text := strings.TrimSpace(strings.Join(msgParts, "\n"))
		if text == "" {
			continue
		}
		if role == "model" {
			parts = append(parts, "[Assistant]: "+text)
		} else {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

func collectGoogleToolDefs(groups []googleToolDeclGroup) []map[string]any {
	out := make([]map[string]any, 0, len(groups))
	for _, group := range groups {
		for _, fn := range group.FunctionDeclarations {
			if strings.TrimSpace(fn.Name) == "" {
				continue
			}
			item := map[string]any{
				"name":        fn.Name,
				"description": fn.Description,
			}
			if len(fn.Parameters) > 0 {
				item["parameters"] = fn.Parameters
			} else if len(fn.ParametersJSONSchema) > 0 {
				item["parameters"] = fn.ParametersJSONSchema
			}
			out = append(out, item)
		}
	}
	return out
}

func buildSystemInstruction(inst googleSystemInstruction) string {
	if len(inst.Parts) == 0 {
		return ""
	}
	texts := make([]string, 0, len(inst.Parts))
	for _, part := range inst.Parts {
		if strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, " "))
}

func buildGoogleToolPrompt(toolDefs []map[string]any) string {
	raw, _ := json.MarshalIndent(toolDefs, "", "  ")
	return "# Tool Use\n\n" +
		"You can call the following tools to help accomplish tasks.\n\n" +
		"Call format (use this exact format):\n" +
		"```function_call\n" +
		"{\"name\": \"<tool_name>\", \"args\": {<arguments>}}\n" +
		"```\n\n" +
		"When calling tools:\n" +
		"- Output ONLY the function_call block(s), nothing else\n" +
		"- You may call multiple tools with multiple blocks\n\n" +
		"Available tools:\n" + string(raw)
}

func buildGoogleToolChoiceInstruction(cfg googleToolConfig) string {
	mode := strings.ToUpper(strings.TrimSpace(cfg.FunctionCallingConfig.Mode))
	switch mode {
	case "NONE":
		return "\n\nIMPORTANT: Do NOT call any tools. Respond with text only."
	case "ANY":
		if len(cfg.FunctionCallingConfig.AllowedFunctionNames) > 0 {
			names := make([]string, 0, len(cfg.FunctionCallingConfig.AllowedFunctionNames))
			for _, name := range cfg.FunctionCallingConfig.AllowedFunctionNames {
				if strings.TrimSpace(name) != "" {
					names = append(names, fmt.Sprintf("%q", name))
				}
			}
			if len(names) > 0 {
				return "\n\nIMPORTANT: You MUST call one of these tools: " + strings.Join(names, ", ") + ". Do not respond with text only."
			}
		}
		return "\n\nIMPORTANT: You MUST call at least one tool. Do not respond with text only."
	default:
		return ""
	}
}

func parseGoogleFunctionCalls(text string) (string, []parsedGoogleFunctionCall) {
	calls := make([]parsedGoogleFunctionCall, 0, 2)
	clean := strings.TrimSpace(text)

	clean = parseGoogleFunctionCallsByPattern(clean, functionCallBlockPattern, &calls)
	clean = parseGoogleFunctionCallsByPattern(clean, functionCallInlinePattern, &calls)

	if len(calls) == 0 && strings.HasPrefix(strings.TrimSpace(clean), "{") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(clean), &raw); err == nil {
			if call, ok := toGoogleFunctionCall(raw); ok {
				calls = append(calls, call)
				clean = ""
			}
		}
	}
	return strings.TrimSpace(clean), calls
}

func parseGoogleFunctionCallsByPattern(text string, pattern *regexp.Regexp, calls *[]parsedGoogleFunctionCall) string {
	matches := pattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return text
	}
	var cleaned strings.Builder
	last := 0
	for _, idx := range matches {
		start, end := idx[0], idx[1]
		groupStart, groupEnd := idx[2], idx[3]
		if start > last {
			cleaned.WriteString(text[last:start])
		}
		last = end
		block := strings.TrimSpace(text[groupStart:groupEnd])
		var raw map[string]any
		if err := json.Unmarshal([]byte(block), &raw); err != nil {
			continue
		}
		if call, ok := toGoogleFunctionCall(raw); ok {
			*calls = append(*calls, call)
		}
	}
	if last < len(text) {
		cleaned.WriteString(text[last:])
	}
	return cleaned.String()
}

func toGoogleFunctionCall(raw map[string]any) (parsedGoogleFunctionCall, bool) {
	name, _ := raw["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return parsedGoogleFunctionCall{}, false
	}
	args, _ := raw["args"].(map[string]any)
	if len(args) == 0 {
		args, _ = raw["arguments"].(map[string]any)
	}
	if args == nil {
		args = map[string]any{}
	}
	return parsedGoogleFunctionCall{Name: name, Args: args}, true
}
