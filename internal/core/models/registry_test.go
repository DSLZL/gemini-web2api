package models_test

import (
	"slices"
	"testing"

	"gemini-web2api/internal/core/models"
)

func TestResolveFallbackUnknownModel(t *testing.T) {
	t.Parallel()

	got, err := models.Resolve("gemini-unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != models.DefaultModelName {
		t.Fatalf("unexpected fallback model: got %q want %q", got.Name, models.DefaultModelName)
	}
	if got.Mode != 1 || got.Think != 4 {
		t.Fatalf("unexpected fallback mode/think: mode=%d think=%d", got.Mode, got.Think)
	}
}

func TestResolveSupportsThinkAtOverride(t *testing.T) {
	t.Parallel()

	got, err := models.Resolve("gemini-3.5-flash-thinking@think=2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "gemini-3.5-flash-thinking" {
		t.Fatalf("unexpected model name: %q", got.Name)
	}
	if got.Mode != 2 || got.Think != 2 {
		t.Fatalf("unexpected mode/think: mode=%d think=%d", got.Mode, got.Think)
	}
}

func TestResolveRejectsInvalidThinkAtOverride(t *testing.T) {
	t.Parallel()

	_, err := models.Resolve("gemini-3.5-flash@think=abc")
	if err == nil {
		t.Fatal("expected invalid think level error")
	}
}

func TestResolveSuffixMappingStillSupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		model     string
		wantMode  int
		wantThink int
	}{
		{name: "max", model: "gemini-3.5-flash-thinking-max", wantMode: 2, wantThink: 0},
		{name: "xhigh", model: "gemini-3.5-flash-thinking-xhigh", wantMode: 2, wantThink: 1},
		{name: "high", model: "gemini-3.5-flash-thinking-high", wantMode: 2, wantThink: 2},
		{name: "medium", model: "gemini-3.5-flash-thinking-medium", wantMode: 2, wantThink: 3},
		{name: "low", model: "gemini-3.5-flash-thinking-low", wantMode: 2, wantThink: 4},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := models.Resolve(tc.model)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Mode != tc.wantMode || got.Think != tc.wantThink {
				t.Fatalf("unexpected mode/think: got=(%d,%d) want=(%d,%d)", got.Mode, got.Think, tc.wantMode, tc.wantThink)
			}
		})
	}
}

func TestResolveUnsupportedThinkSuffix(t *testing.T) {
	t.Parallel()

	_, err := models.Resolve("gemini-3.5-flash-thinking-ultra")
	if err == nil {
		t.Fatal("expected unsupported think suffix error")
	}
}

func TestResolveProEnhancedExtraFields(t *testing.T) {
	t.Parallel()

	got, err := models.Resolve("gemini-3.1-pro-enhanced")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != 3 || got.Think != 4 {
		t.Fatalf("unexpected mode/think: mode=%d think=%d", got.Mode, got.Think)
	}
	if len(got.ExtraFields) != 2 || got.ExtraFields[31] != 2 || got.ExtraFields[80] != 3 {
		t.Fatalf("unexpected extra fields: %#v", got.ExtraFields)
	}
}

func TestPublicModelNamesIncludePro(t *testing.T) {
	t.Parallel()

	names := models.PublicModelNames()
	if !slices.Contains(names, "gemini-3.1-pro") {
		t.Fatalf("expected pro model in public list: %v", names)
	}
	if !slices.Contains(names, "gemini-3.1-pro-enhanced") {
		t.Fatalf("expected pro enhanced model in public list: %v", names)
	}
}
