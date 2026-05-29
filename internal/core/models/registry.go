package models

import (
	"errors"
	"fmt"
	"strings"
)

// DefaultModelName is used when client passes unknown/empty model name.
const DefaultModelName = "gemini-3.5-flash"

var (
	errLegacyThink            = errors.New("legacy @think is not supported; use suffix: -low/-medium/-high/-xhigh/-max")
	errProModel               = errors.New("model gemini-3.1-pro is not available")
	errUnknown                = errors.New("unknown model")
	errUnsupportedThinkSuffix = errors.New("unsupported think suffix")
)

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
		Think:       4,
		Description: "Deep thinking mode",
	},
	"gemini-auto": {
		Mode:        3,
		Think:       4,
		Description: "Auto model selection",
	},
	"gemini-3.5-flash-thinking-lite": {
		Mode:        4,
		Think:       4,
		Description: "Dynamic thinking with adaptive depth",
	},
	"gemini-flash-lite": {
		Mode:        5,
		Think:       4,
		Description: "Lightweight fast model",
	},
}

var publicModelOrder = []string{
	"gemini-3.5-flash",
	"gemini-3.5-flash-thinking",
	"gemini-3.5-flash-thinking-max",
	"gemini-3.5-flash-thinking-xhigh",
	"gemini-3.5-flash-thinking-high",
	"gemini-3.5-flash-thinking-medium",
	"gemini-3.5-flash-thinking-low",
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
// - rejects @think override, keep suffix-only think levels
// - supports suffix override (-low/-medium/-high/-xhigh/-max)
// - unknown model returns error; explicit pro model rejects
func Resolve(input string) (Resolved, error) {
	modelName := strings.TrimSpace(input)

	if strings.Contains(modelName, "@think=") {
		return Resolved{}, errLegacyThink
	}
	if modelName == "" {
		return Resolved{}, errUnknown
	}
	if modelName == "gemini-3.1-pro" || modelName == "gemini-3.1-pro-enhanced" {
		return Resolved{}, errProModel
	}

	if cfg, ok := modelConfigs[modelName]; ok {
		return Resolved{
			Name:        modelName,
			Mode:        cfg.Mode,
			Think:       cfg.Think,
			ExtraFields: cloneExtraFields(cfg.ExtraFields),
		}, nil
	}

	base, parsedThink, parsed, err := parseThinkSuffix(modelName)
	if err != nil {
		return Resolved{}, err
	}
	if !parsed {
		return Resolved{}, errUnknown
	}
	if base == "gemini-3.1-pro" || base == "gemini-3.1-pro-enhanced" {
		return Resolved{}, errProModel
	}

	cfg, ok := modelConfigs[base]
	if !ok {
		return Resolved{}, errUnknown
	}

	return Resolved{
		Name:        base,
		Mode:        cfg.Mode,
		Think:       parsedThink,
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
		if _, exists := modelConfigs[baseName]; exists || baseName == "gemini-3.1-pro" || baseName == "gemini-3.1-pro-enhanced" {
			return "", 0, false, fmt.Errorf("%w: %s", errUnsupportedThinkSuffix, suffix)
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
			continue
		}
		if _, _, parsed, err := parseThinkSuffix(name); err == nil && parsed {
			out = append(out, name)
		}
	}
	return out
}
