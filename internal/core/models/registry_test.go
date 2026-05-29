package models_test

import (
	"strings"
	"testing"

	"gemini-web2api/internal/core/models"
)

func TestResolveRejectsPro(t *testing.T) {
	t.Parallel()

	_, err := models.Resolve("gemini-3.1-pro")
	if err == nil {
		t.Fatal("expected pro model rejection")
	}
}

func TestResolveRejectsLegacyAtThink(t *testing.T) {
	t.Parallel()

	_, err := models.Resolve("gemini-3.5-flash-thinking@think=2")
	if err == nil {
		t.Fatal("expected legacy @think rejection")
	}
	if !strings.Contains(err.Error(), "suffix") {
		t.Fatalf("expected guidance to use suffix format, got %q", err.Error())
	}
}

func TestResolveSuffixMapping(t *testing.T) {
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
			if got.Mode != tc.wantMode {
				t.Fatalf("unexpected mode value, got %d want %d", got.Mode, tc.wantMode)
			}
			if got.Think != tc.wantThink {
				t.Fatalf("unexpected think value, got %d want %d", got.Think, tc.wantThink)
			}
		})
	}
}

func TestResolveDefaultThink(t *testing.T) {
	t.Parallel()

	got, err := models.Resolve("gemini-3.5-flash-thinking")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Mode != 2 {
		t.Fatalf("unexpected mode, got %d want 2", got.Mode)
	}
	if got.Think != 4 {
		t.Fatalf("unexpected default think, got %d want 4", got.Think)
	}
}

func TestResolveUnsupportedThinkSuffix(t *testing.T) {
	t.Parallel()

	_, err := models.Resolve("gemini-3.5-flash-thinking-ultra")
	if err == nil {
		t.Fatal("expected unsupported think suffix rejection")
	}
	if !strings.Contains(err.Error(), "unsupported think suffix") {
		t.Fatalf("expected unsupported think suffix error, got %q", err.Error())
	}
}

func TestResolveUnknownModel(t *testing.T) {
	t.Parallel()

	_, err := models.Resolve("gemini-unknown")
	if err == nil {
		t.Fatal("expected unknown model rejection")
	}
}

func TestResolveBoundaryInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         string
		wantMode      int
		wantThink     int
		wantErrSubstr string
	}{
		{name: "empty", input: "", wantErrSubstr: "unknown model"},
		{name: "suffix_only", input: "-low", wantErrSubstr: "unknown model"},
		{name: "trim_whitespace", input: "  gemini-3.5-flash-thinking  ", wantMode: 2, wantThink: 4},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := models.Resolve(tc.input)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErrSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Mode != tc.wantMode {
				t.Fatalf("unexpected mode, got %d want %d", got.Mode, tc.wantMode)
			}
			if got.Think != tc.wantThink {
				t.Fatalf("unexpected think, got %d want %d", got.Think, tc.wantThink)
			}
		})
	}
}

func TestPublicModelNamesDoesNotContainPro(t *testing.T) {
	t.Parallel()

	names := models.PublicModelNames()
	for _, name := range names {
		if name == "gemini-3.1-pro" || name == "gemini-3.1-pro-enhanced" {
			t.Fatalf("unexpected pro model in public list: %v", names)
		}
	}
}

func TestPublicModelNamesContainsThinkingVariants(t *testing.T) {
	t.Parallel()

	want := []string{
		"gemini-3.5-flash-thinking-low",
		"gemini-3.5-flash-thinking-medium",
		"gemini-3.5-flash-thinking-high",
		"gemini-3.5-flash-thinking-xhigh",
		"gemini-3.5-flash-thinking-max",
	}
	names := models.PublicModelNames()
	index := make(map[string]struct{}, len(names))
	for _, name := range names {
		index[name] = struct{}{}
	}
	for _, variant := range want {
		if _, ok := index[variant]; !ok {
			t.Fatalf("expected thinking variant in public list: %s, got: %v", variant, names)
		}
	}
}
