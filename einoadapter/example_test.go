package einoadapter_test

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/gtkit/go-llm-provider/einoadapter"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

type echoProvider struct{}

func (echoProvider) Name() provider.ProviderName { return provider.ProviderOpenAI }
func (echoProvider) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "hello"}, nil
}
func (echoProvider) ChatStream(context.Context, *provider.ChatRequest) (*provider.StreamReader, error) {
	sent := false
	return provider.NewStreamReader(func() (*provider.StreamChunk, error) {
		if sent {
			return nil, io.EOF
		}
		sent = true
		return &provider.StreamChunk{Delta: "hello"}, nil
	}, nil), nil
}

func ExampleChatModel_Generate() {
	m, err := einoadapter.New(echoProvider{})
	if err != nil {
		fmt.Println(err)
		return
	}
	messages, opts, err := einoadapter.FromRequest(&provider.ChatRequest{Messages: []provider.Message{provider.UserText("hello")}, Temperature: new(float32(0))})
	if err != nil {
		fmt.Println(err)
		return
	}
	message, err := m.Generate(context.Background(), messages, opts...)
	if err != nil {
		fmt.Println(err)
		return
	}
	response, err := einoadapter.ToResponse(message)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(response.Content)
	// Output: hello
}

func ExampleStreamControl() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	control, err := einoadapter.NewStreamControl(cancel)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func() {
		if err := control.Close(); err != nil {
			fmt.Println(err)
		}
	}()
	m, err := einoadapter.New(echoProvider{})
	if err != nil {
		fmt.Println(err)
		return
	}
	reader, err := m.Stream(ctx, nil, einoadapter.WithStreamControl(control))
	if err != nil {
		fmt.Println(err)
		return
	}
	defer reader.Close()
	opening, err := reader.Recv()
	if err != nil {
		fmt.Println(err)
		return
	}
	if _, err = einoadapter.OpeningMetadata(opening); err != nil {
		fmt.Println(err)
		return
	}
	for {
		message, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			fmt.Println(err)
			return
		}
		chunk, err := einoadapter.ToChunk(message)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Print(chunk.Delta)
	}
	fmt.Println()
	// Output: hello
}
