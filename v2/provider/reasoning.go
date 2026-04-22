package provider

// Thinking controls provider-specific reasoning behavior.
type Thinking struct {
	// Enabled explicitly enables or disables provider-side thinking when supported.
	Enabled *bool
	// Effort controls reasoning depth for providers that expose effort levels.
	Effort string
}

const (
	// ThinkingEffortLow requests lighter reasoning effort.
	ThinkingEffortLow = "low"
	// ThinkingEffortMedium requests medium reasoning effort.
	ThinkingEffortMedium = "medium"
	// ThinkingEffortHigh requests deeper reasoning effort.
	ThinkingEffortHigh = "high"
)
