package provider

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserIDContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := WithUserID(t.Context(), "u1")
	userID, ok := UserIDFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "u1", userID)

	_, ok = UserIDFromContext(t.Context())
	assert.False(t, ok)

	_, ok = UserIDFromContext(WithUserID(t.Context(), ""))
	assert.False(t, ok, "空 userID 视为未设置")
}

func TestConversationIDContextRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := WithConversationID(t.Context(), "c1")
	conversationID, ok := ConversationIDFromContext(ctx)
	assert.True(t, ok)
	assert.Equal(t, "c1", conversationID)

	_, ok = ConversationIDFromContext(t.Context())
	assert.False(t, ok)
}

type recordingRecorder struct {
	mu      sync.Mutex
	entries []RecordEntry
}

func (r *recordingRecorder) Record(_ context.Context, entry RecordEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return nil
}

func (r *recordingRecorder) all() []RecordEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]RecordEntry(nil), r.entries...)
}

func TestNewBillingHookRecordsChatEvent(t *testing.T) {
	t.Parallel()

	rec := &recordingRecorder{}
	hook := NewBillingHook(rec)

	ctx := WithConversationID(WithUserID(t.Context(), "u1"), "c1")
	hook(ctx, ObserveEvent{
		Operation: ObserveOperationChat,
		Provider:  ProviderOpenAI,
		Model:     "gpt-4o",
		RequestID: "req-1",
		Usage:     Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		Duration:  time.Second,
	})

	entries := rec.all()
	require.Len(t, entries, 1)
	entry := entries[0]
	assert.Equal(t, "u1", entry.UserID)
	assert.Equal(t, "c1", entry.ConversationID)
	assert.Equal(t, "req-1", entry.RequestID)
	assert.Equal(t, "gpt-4o", entry.Model)
	assert.Equal(t, 15, entry.Usage.TotalTokens)
	assert.False(t, entry.Streaming)
	assert.False(t, entry.Terminated)
}

func TestNewBillingHookSkipsAndTerminatedSemantics(t *testing.T) {
	t.Parallel()

	rec := &recordingRecorder{}
	hook := NewBillingHook(rec)
	ctx := WithUserID(t.Context(), "u1")

	// 流创建事件不含 usage，跳过。
	hook(ctx, ObserveEvent{Operation: ObserveOperationStream})
	// 无 userID 跳过。
	hook(t.Context(), ObserveEvent{Operation: ObserveOperationChat})
	assert.Empty(t, rec.all())

	// 提前关闭的流：Terminated=true 且保留终止原因。
	hook(ctx, ObserveEvent{
		Operation:    ObserveOperationStreamComplete,
		StreamFinish: StreamFinishClosed,
	})
	entries := rec.all()
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Streaming)
	assert.True(t, entries[0].Terminated)
	assert.Equal(t, StreamFinishClosed, entries[0].TerminateReason)

	// 正常 EOF：Terminated=false。
	hook(ctx, ObserveEvent{
		Operation:    ObserveOperationStreamComplete,
		StreamFinish: StreamFinishEOF,
		Usage:        Usage{TotalTokens: 7},
	})
	entries = rec.all()
	require.Len(t, entries, 2)
	assert.False(t, entries[1].Terminated)
}

func TestCombineObserveHooks(t *testing.T) {
	t.Parallel()

	var order []string
	hook := CombineObserveHooks(
		func(context.Context, ObserveEvent) { order = append(order, "a") },
		nil,
		func(context.Context, ObserveEvent) { order = append(order, "b") },
	)
	hook(t.Context(), ObserveEvent{})
	assert.Equal(t, []string{"a", "b"}, order)
}

func TestMemoryUsageStoreAggregatesByUserAndConversation(t *testing.T) {
	t.Parallel()

	store := NewMemoryUsageStore()
	ctx := t.Context()

	require.NoError(t, store.Record(ctx, RecordEntry{
		UserID: "u1", ConversationID: "c1",
		Usage: Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}))
	require.NoError(t, store.Record(ctx, RecordEntry{
		UserID: "u1", ConversationID: "c1",
		Usage:      Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28},
		Terminated: true,
	}))
	require.NoError(t, store.Record(ctx, RecordEntry{
		UserID: "u1", ConversationID: "c2",
		Usage: Usage{TotalTokens: 3},
	}))

	user, ok := store.UserTotals("u1")
	require.True(t, ok)
	assert.Equal(t, 46, user.Usage.TotalTokens)
	assert.Equal(t, 3, user.Calls)
	assert.Equal(t, 1, user.TerminatedCalls)

	conv, ok := store.ConversationTotals("u1", "c1")
	require.True(t, ok)
	assert.Equal(t, 43, conv.Usage.TotalTokens)
	assert.Equal(t, 2, conv.Calls)

	_, ok = store.ConversationTotals("u1", "missing")
	assert.False(t, ok)
	_, ok = store.UserTotals("missing")
	assert.False(t, ok)
}

func TestMemoryUsageStoreConcurrentRecord(t *testing.T) {
	t.Parallel()

	store := NewMemoryUsageStore()
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			_ = store.Record(context.Background(), RecordEntry{
				UserID: "u1", ConversationID: "c1",
				Usage: Usage{TotalTokens: 1},
			})
		})
	}
	wg.Wait()

	totals, ok := store.UserTotals("u1")
	require.True(t, ok)
	assert.Equal(t, 50, totals.Usage.TotalTokens)
	assert.Equal(t, 50, totals.Calls)
}

// TestBillingHookEndToEndWithObservability 验证从 WithObservability 到
// MemoryUsageStore 的完整归账链路：业务调用点零统计代码。
func TestBillingHookEndToEndWithObservability(t *testing.T) {
	t.Parallel()

	store := NewMemoryUsageStore()
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{
				Content: "ok",
				Usage:   Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12},
			}, nil
		},
		chatStream: func(context.Context, *ChatRequest) (*StreamReader, error) {
			sent := false
			return NewStreamReader(func() (*StreamChunk, error) {
				if sent {
					return nil, io.EOF
				}
				sent = true
				return &StreamChunk{Delta: "hi", FinishReason: "stop", Usage: Usage{TotalTokens: 5}}, nil
			}, nil), nil
		},
	}
	billed := WithObservability(p, ObserveOptions{OnEvent: NewBillingHook(store)})

	ctx := WithConversationID(WithUserID(t.Context(), "u1"), "c1")
	_, err := billed.Chat(ctx, &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)

	stream, err := billed.ChatStream(ctx, &ChatRequest{Messages: []Message{UserText("hi")}})
	require.NoError(t, err)
	for {
		if _, err := stream.Recv(); err != nil {
			break
		}
	}
	require.NoError(t, stream.Close())

	conv, ok := store.ConversationTotals("u1", "c1")
	require.True(t, ok)
	assert.Equal(t, 17, conv.Usage.TotalTokens, "非流式 12 + 流式 5")
	assert.Equal(t, 2, conv.Calls)
}
