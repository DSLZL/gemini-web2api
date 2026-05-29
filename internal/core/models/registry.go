package models

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultModelName is used when client passes unknown/empty model name.
const DefaultModelName = "gemini-3.5-flash"

// Resolved describes the final model config used for one upstream call.
type Resolved struct {
	Name        string
	Mode        int
	Think       int
	ExtraFields map[int]any
}

type config struct {
	Mode        int
	Think       int
	Description string
	ExtraFields map[int]any
}

var modelConfigs = map[string]config{
	"gemini-3.5-flash": {
		Mode:        1,
		Think:       4,
		Description: "Fast general-purpose model",
	},
	"gemini-3.5-flash-thinking": {
		Mode:        2,
		Think:       0,
		Description: "Deep thinking mode",
	},
	"gemini-3.1-pro": {
		Mode:        3,
		Think:       4,
		Description: "Pro model",
	},
	"gemini-3.1-pro-enhanced": {
		Mode:        3,
		Think:       4,
		Description: "Pro with enhanced output",
		ExtraFields: map[int]any{
			31: 2,
			80: 3,
		},
	},
	"gemini-auto": {
		Mode:        4,
		Think:       4,
		Description: "Auto model selection",
	},
	"gemini-3.5-flash-thinking-lite": {
		Mode:        5,
		Think:       0,
		Description: "Dynamic thinking with adaptive depth",
	},
	"gemini-flash-lite": {
		Mode:        6,
		Think:       4,
		Description: "Lightweight fast model",
	},
}

var publicModelOrder = []string{
	"gemini-3.5-flash",
	"gemini-3.5-flash-thinking",
	"gemini-3.1-pro",
	"gemini-3.1-pro-enhanced",
	"gemini-auto",
	"gemini-3.5-flash-thinking-lite",
	"gemini-flash-lite",
}

// Keep suffix support for compatibility with older clients.
var suffixThink = map[string]int{
	"max":    0,
	"xhigh":  1,
	"high":   2,
	"medium": 3,
	"low":    4,
}

// Resolve parses model name and think override.
//
// Behavior:
// - supports @think=<int> override
// - supports legacy suffix override (-low/-medium/-high/-xhigh/-max)
// - unknown/empty model falls back to DefaultModelName
func Resolve(input string) (Resolved, error) {
	modelName := strings.TrimSpace(input)
	thinkOverride := -1

	if idx := strings.LastIndex(modelName, "@think="); idx >= 0 {
		thinkRaw := strings.TrimSpace(modelName[idx+len("@think="):])
		if thinkRaw == "" {
			return Resolved{}, fmt.Errorf("invalid think level: %s", thinkRaw)
		}
		value, err := strconv.Atoi(thinkRaw)
		if err != nil {
			return Resolved{}, fmt.Errorf("invalid think level: %s", thinkRaw)
		}
		thinkOverride = value
		modelName = strings.TrimSpace(modelName[:idx])
	}

	_, exactMatch := modelConfigs[modelName]
	if thinkOverride < 0 && !exactMatch {
		if base, think, parsed, err := parseThinkSuffix(modelName); err != nil {
			return Resolved{}, err
		} else if parsed {
			modelName = base
			thinkOverride = think
		}
	}

	if modelName == "" {
		modelName = DefaultModelName
	}

	cfg, ok := modelConfigs[modelName]
	if !ok {
		modelName = DefaultModelName
		cfg = modelConfigs[DefaultModelName]
	}

	think := cfg.Think
	if thinkOverride >= 0 {
		think = thinkOverride
	}

	return Resolved{
		Name:        modelName,
		Mode:        cfg.Mode,
		Think:       think,
		ExtraFields: cloneExtraFields(cfg.ExtraFields),
	}, nil
}

func parseThinkSuffix(modelName string) (base string, think int, parsed bool, err error) {
	idx := strings.LastIndex(modelName, "-")
	if idx <= 0 || idx >= len(modelName)-1 {
		return "", 0, false, nil
	}

	suffix := modelName[idx+1:]
	value, ok := suffixThink[suffix]
	if !ok {
		baseName := modelName[:idx]
		if _, exists := modelConfigs[baseName]; exists {
			return "", 0, false, fmt.Errorf("unsupported think suffix: %s", suffix)
		}
		return "", 0, false, nil
	}

	baseName := modelName[:idx]
	if _, exists := modelConfigs[baseName]; !exists {
		return "", 0, false, nil
	}
	return baseName, value, true, nil
}

func cloneExtraFields(in map[int]any) map[int]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func PublicModelNames() []string {
	out := make([]string, 0, len(publicModelOrder))
	for _, name := range publicModelOrder {
		if _, ok := modelConfigs[name]; ok {
			out = append(out, name)
		}
	}
	return out
}
