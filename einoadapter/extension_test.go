package einoadapter

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

func TestContentEmptyFormsAndStaleExtension(t *testing.T) {
	t.Parallel()
	for _, parts := range [][]provider.ContentPart{nil, {}, {provider.TextPart("")}} {
		original := provider.Message{Role: provider.RoleTool, Content: parts, ToolCallID: "call"}
		message, err := inputMessage(original)
		if err != nil {
			t.Fatal(err)
		}
		got, err := messageToProvider(message)
		if err != nil {
			t.Fatal(err)
		}
		equal(t, original, got)
	}
	message, err := inputMessage(provider.UserMessage(provider.TextPart("a"), provider.TextPart("b")))
	if err != nil {
		t.Fatal(err)
	}
	message.UserInputMultiContent[0].Text = "changed"
	if _, err = messageToProvider(message); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestMalformedExtensionsRejected(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		`{"v":1,"kind":"chunk","unknown":1}` + "\n",
		`{"v":1,"kind":"chunk"} {"v":1,"kind":"chunk"}` + "\n",
		`{"v":1,"kind":"chunk","metadata":{}}` + "\n",
		`{"v":1,"kind":"chunk","has_content":true}` + "\n",
		`{"v":1,"kind":"input","usage":{}}` + "\n",
		`{"v":1,"kind":"response","signature":"s"}` + "\n",
		`{"v":1,"kind":"opening","metadata":{},"model":"bad"}` + "\n",
		`{"v":1,"kind":"opening"}` + "\n",
	} {
		if _, err := readEvents(map[string]any{eventKey: text}); !errors.Is(err, provider.ErrInvalidRequest) {
			t.Fatalf("accepted %q: %v", text, err)
		}
	}
	metadata := provider.ResponseMetadata{}
	extra, err := eventExtra(event{Kind: "opening", Metadata: &metadata})
	if err != nil {
		t.Fatal(err)
	}
	opening := &schema.Message{Role: schema.Assistant, Extra: extra}
	if _, err = ToResponse(opening); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
	joined, err := schema.ConcatMessages([]*schema.Message{opening, opening})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ToResponse(joined); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err = OpeningMetadata(&schema.Message{Content: "not an opening"}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestMetadataAllowlist(t *testing.T) {
	t.Parallel()
	metadata := provider.ResponseMetadata{Headers: http.Header{"Authorization": {"fixture-secret"}, "Set-Cookie": {"fixture-cookie"}, "X-Request-Id": {"request"}}}
	extra, err := eventExtra(event{Kind: "opening", Metadata: &metadata})
	if err != nil {
		t.Fatal(err)
	}
	events, err := readEvents(extra)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, http.Header{"X-Request-Id": {"request"}}, events[0].Metadata.Headers)
	equal(t, 3, len(metadata.Headers))
}
