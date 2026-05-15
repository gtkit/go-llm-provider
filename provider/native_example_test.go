package provider_test

import (
	"fmt"

	"github.com/gtkit/go-llm-provider/provider"
)

func ExampleNewAnthropicProvider() {
	p, err := provider.NewAnthropicProvider(provider.NativeProviderConfig{
		APIKey: "anthropic-api-key",
		Model:  "claude-sonnet-4-5",
	})
	fmt.Println(p != nil, err == nil)
	// Output: true true
}

func ExampleNewGeminiProvider() {
	p, err := provider.NewGeminiProvider(provider.NativeProviderConfig{
		APIKey: "gemini-api-key",
		Model:  "gemini-2.5-flash",
	})
	fmt.Println(p != nil, err == nil)
	// Output: true true
}

func ExampleNewProviderFromPreset_native() {
	claude, claudeErr := provider.NewProviderFromPreset(provider.ProviderAnthropic, "anthropic-api-key", "")
	gemini, geminiErr := provider.NewProviderFromPreset(provider.ProviderGemini, "gemini-api-key", "")
	fmt.Println(claude.Name(), claudeErr == nil)
	fmt.Println(gemini.Name(), geminiErr == nil)
	// Output:
	// anthropic true
	// gemini true
}
