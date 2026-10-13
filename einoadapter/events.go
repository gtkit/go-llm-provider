package einoadapter

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gtkit/go-llm-provider/v2/provider"
	"github.com/gtkit/json/v2"
)

const eventKey = "go_llm_provider_events_v1"

// Events supplement native Eino fields. Text deltas are never copied here.
// Usage is retained in full because Eino concat takes per-field maxima, whereas
// the Provider contract takes the last nonzero complete snapshot.
type event struct {
	HasContent   bool                       `json:"has_content,omitempty"`
	Version      int                        `json:"v"`
	Kind         string                     `json:"kind"`
	Content      *[]provider.ContentPart    `json:"content,omitempty"`
	Blocks       []provider.ThinkingBlock   `json:"blocks,omitempty"`
	Metadata     *provider.ResponseMetadata `json:"metadata,omitempty"`
	Model        string                     `json:"model,omitempty"`
	Usage        *provider.Usage            `json:"usage,omitempty"`
	Parts        []provider.ContentPart     `json:"parts,omitempty"`
	Search       *provider.SearchMetadata   `json:"search,omitempty"`
	Signature    string                     `json:"signature,omitempty"`
	ReasoningEnd int                        `json:"reasoning_end,omitempty"`
	Redacted     string                     `json:"redacted,omitempty"`
	Tools        []toolSignature            `json:"tools,omitempty"`
}

type toolSignature struct {
	Index int    `json:"index"`
	Value string `json:"value"`
}

func eventExtra(e event) (map[string]any, error) {
	e.Version = 1
	if e.Metadata != nil {
		metadata := *e.Metadata
		metadata.Headers = metadata.Headers.Clone()
		// Mirror the core's diagnostic allowlist; custom providers must not
		// smuggle Authorization/cookies into a serializable Eino message.
		for key := range metadata.Headers {
			switch strings.ToLower(key) {
			case "x-request-id", "x-requestid", "x-correlation-id", "x-amzn-requestid", "request-id", "openai-request-id", "x-typesafe-request-id", "x-ratelimit-limit-requests", "x-ratelimit-remaining-requests", "x-ratelimit-reset-requests", "x-ratelimit-limit-tokens", "x-ratelimit-remaining-tokens", "x-ratelimit-reset-tokens", "x-ratelimit-limit-reqs":
			default:
				delete(metadata.Headers, key)
			}
		}
		e.Metadata = &metadata
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("einoadapter: encode extension: %w", err)
	}
	return map[string]any{eventKey: string(raw) + "\n"}, nil
}

func readEvents(extra map[string]any) ([]event, error) {
	if len(extra) == 0 {
		return nil, nil
	}
	if len(extra) != 1 {
		return nil, invalid("Extra unknown namespace")
	}
	text, ok := extra[eventKey].(string)
	if !ok || text == "" || !strings.HasSuffix(text, "\n") {
		return nil, invalid("Extra requires framed JSON Lines")
	}
	var events []event
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		var e event
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&e); err != nil {
			return nil, fmt.Errorf("einoadapter: extension: %w: %w", provider.ErrInvalidRequest, err)
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return nil, invalid("Extra line must contain one event")
		}
		if err := validateEvent(e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

func validateEvent(e event) error {
	if e.Version != 1 {
		return invalid("Extra version")
	}
	if e.Kind != "input" && (e.HasContent || e.Content != nil) {
		return invalid("content outside input event")
	}
	if e.Kind != "chunk" && (e.Signature != "" || e.Redacted != "" || e.ReasoningEnd != 0) {
		return invalid("deltas outside chunk event")
	}
	return validateEventFields(e)
}

func validateEventFields(e event) error {
	switch e.Kind {
	case "input":
		if e.Metadata != nil || hasResultPayload(e) || (e.Content != nil && !e.HasContent) {
			return invalid("invalid input event fields")
		}
	case "opening":
		if e.Metadata == nil || hasResultPayload(e) || len(e.Blocks) > 0 || len(e.Tools) > 0 {
			return invalid("invalid opening event fields")
		}
	case "chunk":
		if e.Metadata != nil || len(e.Blocks) > 0 {
			return invalid("invalid chunk event fields")
		}
	case "response":
	default:
		return invalid("Extra event kind")
	}
	return nil
}

func hasResultPayload(e event) bool {
	return e.Model != "" || e.Usage != nil || len(e.Parts) > 0 || e.Search != nil
}
