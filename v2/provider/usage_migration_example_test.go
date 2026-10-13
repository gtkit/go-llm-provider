package provider_test

import (
	"context"
	"fmt"
	"sync"
	"time"
	"uuid"

	"github.com/gtkit/go-llm-provider/v2/provider"
)

type businessCallKey struct{}
type businessCall struct {
	id     string
	userID int64
	once   sync.Once
}

type businessExampleProvider struct{}

func (businessExampleProvider) Name() provider.ProviderName { return provider.ProviderOpenAI }
func (businessExampleProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ok", Usage: provider.Usage{TotalTokens: 3}}, nil
}
func (businessExampleProvider) ChatStream(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
	return nil, provider.ErrUnsupportedCapability
}

// ExampleTryWithObservability_businessUsage 演示应用拥有调用键、身份和写入预算。
// 真实应用在每次模型调用边界创建 businessCall；存储按 id 建唯一索引。
func ExampleTryWithObservability_businessUsage() {
	record := func(ctx context.Context, id string, userID int64, event provider.ObserveEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Printf("user=%d tokens=%d stable_key=%t\n", userID, event.Usage.TotalTokens, id != "")
		return nil
	}
	p, err := provider.TryWithObservability(businessExampleProvider{}, provider.ObserveOptions{OnEvent: func(ctx context.Context, event provider.ObserveEvent) {
		if event.Operation != provider.ObserveOperationChat {
			return
		}
		call, ok := ctx.Value(businessCallKey{}).(*businessCall)
		if !ok {
			return
		}
		call.once.Do(func() {
			writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if err := record(writeCtx, call.id, call.userID, event); err != nil {
				fmt.Println("record failed:", err)
			}
		})
	}})
	if err != nil {
		fmt.Println(err)
		return
	}
	ctx := context.WithValue(context.Background(), businessCallKey{}, &businessCall{id: uuid.New().String(), userID: 42})
	if _, err = p.Chat(ctx, &provider.ChatRequest{Messages: []provider.Message{provider.UserText("hello")}}); err != nil {
		fmt.Println(err)
	}
	// Output: user=42 tokens=3 stable_key=true
}
