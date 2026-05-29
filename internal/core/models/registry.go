package models

import (
	"errors"
	"fmt"
	"strings"
)

var (
	errLegacyThink            = errors.New("legacy @think is not supported; use suffix: -low/-medium/-high/-xhigh/-max")
	errProModel               = errors.New("model gemini-3.1-pro is not available")
	errUnknown                = errors.New("unknown model")
	errUnsupportedThinkSuffix = errors.New("unsupported think suffix")
)

var modelMode = map[string]int{
	"gemini-3.5-flash":               1,
	"gemini-3.5-flash-thinking":      2,
	"gemini-auto":                    3,
	"gemini-3.5-flash-thinking-lite": 4,
	"gemini-flash-lite":              5,
}

var suffixThink = map[string]int{
	"max":    0,
	"xhigh":  1,
	"high":   2,
	"medium": 3,
	"low":    4,
}

// Resolve parses a public model name and optional think-depth suffix.
func Resolve(input string) (mode int, think int, err error) {
	input = strings.TrimSpace(input)

	if strings.Contains(input, "@think=") {
		return 0, 0, errLegacyThink
	}

	if m, resolveErr := resolveBaseModelMode(input); resolveErr == nil {
		return m, 4, nil
	}

	if idx := strings.LastIndex(input, "-"); idx > 0 {
		suffix := input[idx+1:]
		if t, ok := suffixThink[suffix]; ok {
			baseName := input[:idx]
			m, resolveErr := resolveBaseModelMode(baseName)
			if resolveErr != nil {
				return 0, 0, resolveErr
			}
			return m, t, nil
		}

		baseName := input[:idx]
		if _, ok := modelMode[baseName]; ok || baseName == "gemini-3.1-pro" {
			return 0, 0, fmt.Errorf("%w: %s", errUnsupportedThinkSuffix, suffix)
		}
	}

	return 0, 0, errUnknown
}

func resolveBaseModelMode(name string) (int, error) {
	if name == "gemini-3.1-pro" {
		return 0, errProModel
	}
	m, exists := modelMode[name]
	if !exists {
		return 0, errUnknown
	}
	return m, nil
}

func PublicModelNames() []string {
	return []string{
		"gemini-3.5-flash",
		"gemini-3.5-flash-thinking",
		"gemini-auto",
		"gemini-3.5-flash-thinking-lite",
		"gemini-flash-lite",
	}
}
