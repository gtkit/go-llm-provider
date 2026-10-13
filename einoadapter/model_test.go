package einoadapter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
	"github.com/gtkit/json/v2"
)

type fakeProvider struct {
	chat   func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error)
	stream func(context.Context, *provider.ChatRequest) (*provider.StreamReader, error)
}

func (*fakeProvider) Name() provider.ProviderName { return provider.ProviderOpenAI }
func (p *fakeProvider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return p.chat(ctx, req)
}
func (p *fakeProvider) ChatStream(ctx context.Context, req *provider.ChatRequest) (*provider.StreamReader, error) {
	return p.stream(ctx, req)
}

func mustModel(t *testing.T, p provider.Provider) *ChatModel {
	t.Helper()
	m, err := New(p)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func equal[T any](t *testing.T, want, got T) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("want %#v\ngot  %#v", want, got)
	}
}
func requestFixture() *provider.ChatRequest {
	return &provider.ChatRequest{
		Model: "requested", Messages: []provider.Message{
			provider.SystemMessage(provider.WithCacheControl(provider.TextPart("system"), provider.CacheControlEphemeralTTL(provider.CacheControlTTL1h))),
			provider.UserMessage(provider.TextPart("before"), provider.ImageDataPart([]byte{1, 2}, "image/png"), provider.TextPart("between"), provider.ImageURLPart("https://example.com/image"), provider.FileDataPart([]byte{3}, "application/pdf", "f.pdf"), provider.FileURLPart("https://example.com/file", "application/pdf"), provider.FileIDPart("file-1")),
			{Role: provider.RoleAssistant, Reasoning: "think", ThinkingBlocks: []provider.ThinkingBlock{{Thinking: "think", Signature: "sig"}, {Redacted: "hidden"}}, ToolCalls: []provider.ToolCall{{ID: "a", Function: provider.FunctionCall{Name: "lookup", Arguments: `{"q":1}`}, ThoughtSignature: "tool-sig"}}},
			{Role: provider.RoleTool, Content: []provider.ContentPart{provider.TextPart("")}, ToolCallID: "a", ToolName: "lookup"},
		}, MaxTokens: 100, Temperature: new(float32(0)), TopP: new(float32(0)), Stop: []string{"stop"},
		Tools: []provider.Tool{{Function: provider.FunctionDef{Name: "lookup", Description: "lookup", Strict: new(true), Parameters: json.RawMessage(`{"type":"object"}`)}}}, ToolChoice: provider.ToolChoiceFunction{Name: "lookup"}, ParallelToolCalls: new(false), Seed: new(7), CandidateCount: 1,
		Thinking: &provider.Thinking{Enabled: new(true), Effort: "high", BudgetTokens: new(50)}, ResponseFormat: &provider.ResponseFormat{Type: "json_schema", Name: "answer", Schema: json.RawMessage(`{"type":"object"}`), Strict: new(true)}, StreamUsage: new(false), OutputModalities: []provider.Modality{provider.ModalityText},
	}
}
func fullUsage() provider.Usage {
	return provider.Usage{PromptTokens: 90, CompletionTokens: 20, ReasoningTokens: 10, CacheReadTokens: 30, CacheWriteTokens: 20, CacheWrite5mTokens: 12, CacheWrite1hTokens: 8, TotalTokens: 110, WebSearchRequests: 2, WebSearchGroundedPrompts: 1}
}
func responseFixture() *provider.ChatResponse {
	return &provider.ChatResponse{Content: "answer", Reasoning: "think", FinishReason: "tool_calls", Usage: fullUsage(),
		Metadata:       provider.ResponseMetadata{Provider: provider.ProviderOpenAI, Model: "actual", RequestID: "request", Headers: http.Header{"X-Request-Id": []string{"request"}}, StatusCode: 200, Status: "200 OK"},
		ThinkingBlocks: []provider.ThinkingBlock{{Thinking: "think", Signature: "sig"}, {Redacted: "hidden"}}, Parts: []provider.ContentPart{provider.ImageDataPart([]byte{1, 2}, "image/png")},
		Search: &provider.SearchMetadata{Queries: []string{"q"}}, ToolCalls: []provider.ToolCall{{ID: "a", Function: provider.FunctionCall{Name: "lookup", Arguments: `{"q":1}`}, ThoughtSignature: "tool-sig"}},
	}
}

func TestRequestResponseRoundTrip(t *testing.T) {
	t.Parallel()
	want := requestFixture()
	response := responseFixture()
	calls := 0
	m := mustModel(t, &fakeProvider{chat: func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		calls++
		equal(t, want, req)
		return response, nil
	}})
	messages, opts, err := FromRequest(want)
	if err != nil {
		t.Fatal(err)
	}
	// The extension must remain usable after storage/callback JSON replay.
	raw, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	message, err := m.Generate(t.Context(), messages, opts...)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, 1, calls)
	raw, err = json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	got, err := ToResponse(message)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, response, got)
	back, err := messageToProvider(message)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, response.AssistantMessage(), back)
}

func TestRequestSnapshotAndOptionPrecedence(t *testing.T) {
	t.Parallel()
	req := requestFixture()
	messages, opts, err := FromRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	*req.Temperature = 1
	req.Stop[0] = "changed"
	*req.Thinking.Enabled = false
	req.Tools[0].Function.Parameters.(json.RawMessage)[0] = '['
	m := mustModel(t, &fakeProvider{})
	got, _, err := m.request(messages, opts)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, requestFixture(), got)
	got.Stop[0] = "call mutation"
	*got.TopP = 1
	got, _, err = m.request(messages, opts)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, requestFixture(), got)
	got, _, err = m.request(messages, append(opts, model.WithModel("override"), model.WithMaxTokens(0), model.WithTemperature(0), model.WithTopP(0), model.WithStop([]string{}), model.WithTools(nil), model.WithToolChoice(schema.ToolChoiceForbidden)))
	if err != nil {
		t.Fatal(err)
	}
	equal(t, "override", got.Model)
	equal(t, 0, got.MaxTokens)
	equal(t, 0, len(got.Stop))
	equal(t, 0, len(got.Tools))
	equal[provider.ToolChoiceOption](t, provider.ToolChoiceNone, got.ToolChoice)
}

func TestWithToolsConcurrentIsolation(t *testing.T) {
	t.Parallel()
	m := mustModel(t, &fakeProvider{chat: func(_ context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
		if len(req.Tools) != 1 || req.Tools[0].Function.Name != req.Model {
			return nil, fmt.Errorf("tool escaped snapshot: %q", req.Model)
		}
		req.Tools[0].Function.Name = "mutated"
		return &provider.ChatResponse{}, nil
	}})
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			name := fmt.Sprintf("tool_%d", i)
			tool := &schema.ToolInfo{Name: name, ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"q": {Type: schema.String, Required: true}})}
			bound, err := m.WithTools([]*schema.ToolInfo{tool})
			if err != nil {
				t.Error(err)
				return
			}
			tool.Name = "external mutation"
			for range 10 {
				if _, err = bound.Generate(t.Context(), []*schema.Message{{Role: schema.User, Content: "hello"}}, model.WithModel(name)); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	equal(t, 0, len(m.tools))
}

func TestInvalidRequestsNeverCallUpstream(t *testing.T) {
	t.Parallel()
	m := mustModel(t, &fakeProvider{chat: func(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
		t.Error("unexpected upstream request")
		return nil, nil
	}})
	cases := []*schema.Message{nil, {Role: "invalid"}, {Role: schema.User, Extra: map[string]any{"unknown": "x"}}, {Role: schema.User, Extra: map[string]any{eventKey: []string{"x"}}}, {Role: schema.User, Extra: map[string]any{eventKey: "{}\n"}}, {Role: schema.User, Extra: map[string]any{eventKey: "{\n"}}, {Role: schema.User, Extra: map[string]any{eventKey: `{"v":1,"kind":"unknown"}` + "\n"}}, {Role: schema.User, Name: "unsupported"}, {Role: schema.User, UserInputMultiContent: []schema.MessageInputPart{{Type: schema.ChatMessagePartTypeAudioURL}}}}
	for i, msg := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			if _, err := m.Generate(t.Context(), []*schema.Message{msg}); !errors.Is(err, provider.ErrInvalidRequest) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, _, err := FromRequest(nil); !errors.Is(err, provider.ErrNilChatRequest) {
		t.Fatal(err)
	}
	for _, n := range []int{-1, 2} {
		if _, _, err := FromRequest(&provider.ChatRequest{CandidateCount: n}); !errors.Is(err, provider.ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
	if _, err := m.WithTools([]*schema.ToolInfo{{Name: "bad", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"q": nil})}}); !errors.Is(err, provider.ErrInvalidRequest) {
		t.Fatal(err)
	}
	var nilProvider *fakeProvider
	if _, err := New(nilProvider); !errors.Is(err, provider.ErrNilProvider) {
		t.Fatal(err)
	}
}

func TestToolChoiceMapping(t *testing.T) {
	t.Parallel()
	m := mustModel(t, &fakeProvider{})
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "a"}, {Name: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []schema.ToolChoice{schema.ToolChoiceAllowed, schema.ToolChoiceForbidden, schema.ToolChoiceForced} {
		req, _, err := bound.(*ChatModel).request(nil, []model.Option{model.WithToolChoice(choice, "b")})
		if err != nil {
			t.Fatal(err)
		}
		equal(t, "b", req.Tools[0].Function.Name)
		if choice == schema.ToolChoiceForced {
			equal[provider.ToolChoiceOption](t, provider.ToolChoiceFunction{Name: "b"}, req.ToolChoice)
		}
	}
	for _, opts := range [][]model.Option{{model.WithToolChoice("bad")}, {model.WithToolChoice(schema.ToolChoiceAllowed, "missing")}, {model.WithToolChoice(schema.ToolChoiceAllowed, "a", "a")}, {model.WithDeferredTools([]*schema.ToolInfo{{Name: "a"}})}} {
		if _, _, err := bound.(*ChatModel).request(nil, opts); !errors.Is(err, provider.ErrInvalidRequest) {
			t.Fatal(err)
		}
	}
}
