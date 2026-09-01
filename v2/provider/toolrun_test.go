package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolLoopProvider 在第一轮返回一次工具调用，之后返回最终文本，
// 用于驱动 RunToolLoop 走完一轮工具执行。
func toolLoopProvider() *stubProvider {
	var calls int
	return &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, _ *ChatRequest) (*ChatResponse, error) {
			calls++
			if calls == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{
						{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}},
					},
				}, nil
			}
			return &ChatResponse{Content: "done"}, nil
		},
	}
}

func TestRunToolLoopToolRetrySucceedsAfterFailures(t *testing.T) {
	t.Parallel()

	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("transient")
		}
		return "ok", nil
	}

	resp, err := RunToolLoopWithOptions(context.Background(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{
		MaxRounds: 5,
		ToolRetry: ToolRetryOptions{MaxAttempts: 3},
	})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
	assert.Equal(t, 3, attempts)
}

func TestRunToolLoopNoRetryByDefault(t *testing.T) {
	t.Parallel()

	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		return "", errors.New("boom")
	}

	resp, err := RunToolLoopWithOptions(context.Background(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 5})
	require.NoError(t, err)
	// 默认不重试：handler 仅执行一次，错误编码回模型后流程继续。
	assert.Equal(t, 1, attempts)
	assert.Equal(t, "done", resp.Content)
}

func TestRunToolLoopToolRetryRespectsShouldRetry(t *testing.T) {
	t.Parallel()

	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		return "", errors.New("boom")
	}

	_, err := RunToolLoopWithOptions(context.Background(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{
		MaxRounds: 5,
		ToolRetry: ToolRetryOptions{
			MaxAttempts: 3,
			ShouldRetry: func(error) bool { return false },
		},
	})
	require.NoError(t, err)
	// ShouldRetry 返回 false：尽管 MaxAttempts=3，也只尝试一次。
	assert.Equal(t, 1, attempts)
}

func TestRunToolLoopToolRetryStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var attempts int
	handler := func(context.Context, string, string) (string, error) {
		attempts++
		cancel()
		return "", context.Canceled
	}

	_, err := RunToolLoopWithOptions(ctx, toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{
		MaxRounds: 5,
		ToolRetry: ToolRetryOptions{MaxAttempts: 3},
	})
	require.Error(t, err)
	// context 取消不重试。
	assert.Equal(t, 1, attempts)
}

func TestRunToolLoopExceedsMaxRoundsReportsNormalized(t *testing.T) {
	t.Parallel()

	// provider 始终返回工具调用，循环永不自然结束，直至触发轮数上限。
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{
				ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
			}, nil
		},
	}
	handler := func(context.Context, string, string) (string, error) { return "ok", nil }

	// MaxRounds=0 规范化为 20，错误信息应反映规范化后的轮数。
	_, err := RunToolLoopWithOptions(context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 0})
	require.Error(t, err)
	assert.ErrorContains(t, err, "max rounds (20)")
}

func TestRunToolLoopAccumulatesUsage(t *testing.T) {
	t.Parallel()

	var calls int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			calls++
			if calls == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
					Usage:     Usage{PromptTokens: 10, CompletionTokens: 5, CacheReadTokens: 2, TotalTokens: 15},
				}, nil
			}
			return &ChatResponse{
				Content: "done",
				Usage:   Usage{PromptTokens: 20, CompletionTokens: 8, CacheReadTokens: 3, TotalTokens: 28},
			}, nil
		},
	}
	handler := func(context.Context, string, string) (string, error) { return "ok", nil }

	resp, err := RunToolLoopWithOptions(context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 5, AccumulateUsage: true})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
	// 开启 AccumulateUsage：Usage 为两轮累加（含缓存 token 字段）。
	assert.Equal(t, 30, resp.Usage.PromptTokens)
	assert.Equal(t, 13, resp.Usage.CompletionTokens)
	assert.Equal(t, 5, resp.Usage.CacheReadTokens)
	assert.Equal(t, 43, resp.Usage.TotalTokens)
}

func TestRunToolLoopUsageDefaultsToLastRound(t *testing.T) {
	t.Parallel()

	var calls int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			calls++
			if calls == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
					Usage:     Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
				}, nil
			}
			return &ChatResponse{
				Content: "done",
				Usage:   Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28},
			}, nil
		},
	}
	handler := func(context.Context, string, string) (string, error) { return "ok", nil }

	resp, err := RunToolLoopWithOptions(context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, handler, RunToolLoopOptions{MaxRounds: 5})
	require.NoError(t, err)
	// 默认不累加：保持最后一轮 Usage。
	assert.Equal(t, 28, resp.Usage.TotalTokens)
}

func TestRunToolLoopToolResultTransformerNilPassesThrough(t *testing.T) {
	t.Parallel()

	var requests []*ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
				}, nil
			}
			return &ChatResponse{Content: "done"}, nil
		},
	}

	resp, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		return "raw-result", nil
	}, RunToolLoopOptions{MaxRounds: 2})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)

	lastMessage := requests[1].Messages[len(requests[1].Messages)-1]
	assert.Equal(t, "raw-result", contentText(lastMessage.Content))
}

func TestRunToolLoopAppliesToolResultTransformer(t *testing.T) {
	t.Parallel()

	var requests []*ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
				}, nil
			}
			return &ChatResponse{Content: "done"}, nil
		},
	}

	var seenCall ToolCall
	resp, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		return "raw-result", nil
	}, RunToolLoopOptions{
		MaxRounds: 2,
		ToolResultTransformer: func(_ context.Context, call ToolCall, result string) (string, error) {
			seenCall = call
			return "transformed:" + result, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
	assert.Equal(t, "call_1", seenCall.ID)

	lastMessage := requests[1].Messages[len(requests[1].Messages)-1]
	assert.Equal(t, "transformed:raw-result", contentText(lastMessage.Content))
}

func TestRunToolLoopToolResultTransformerErrorAbortsLoop(t *testing.T) {
	t.Parallel()

	var chatCalls int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			chatCalls++
			return &ChatResponse{
				ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
			}, nil
		},
	}

	boom := errors.New("boom")
	resp, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		return "raw-result", nil
	}, RunToolLoopOptions{
		MaxRounds: 5,
		ToolResultTransformer: func(context.Context, ToolCall, string) (string, error) {
			return "", boom
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	require.ErrorIs(t, err, boom)
	// 转换失败后循环立即中止，不应该发起下一轮请求。
	assert.Equal(t, 1, chatCalls)
}

func TestRunToolLoopToolResultTransformerAppliesOnlyAfterRetrySucceeds(t *testing.T) {
	t.Parallel()

	var requests []*ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
				}, nil
			}
			return &ChatResponse{Content: "done"}, nil
		},
	}

	var attempts int
	var transformerCalls int
	resp, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("transient")
		}
		return "raw-result", nil
	}, RunToolLoopOptions{
		MaxRounds: 5,
		ToolRetry: ToolRetryOptions{MaxAttempts: 3},
		ToolResultTransformer: func(_ context.Context, _ ToolCall, result string) (string, error) {
			transformerCalls++
			return "transformed:" + result, nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
	assert.Equal(t, 3, attempts)
	// 重试期间的失败不应该经过 transformer，只在最终成功的结果上调用一次。
	assert.Equal(t, 1, transformerCalls)

	lastMessage := requests[1].Messages[len(requests[1].Messages)-1]
	assert.Equal(t, "transformed:raw-result", contentText(lastMessage.Content))
}

func TestRunToolLoopParallelToolCallsAppliesTransformerToEachCall(t *testing.T) {
	t.Parallel()

	var requests []*ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			requests = append(requests, req)
			if len(requests) == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{
						{ID: "call_1", Function: FunctionCall{Name: "one", Arguments: `{}`}},
						{ID: "call_2", Function: FunctionCall{Name: "two", Arguments: `{}`}},
					},
				}, nil
			}
			return &ChatResponse{Content: "done"}, nil
		},
	}

	resp, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(_ context.Context, name, _ string) (string, error) {
		return name + "-result", nil
	}, RunToolLoopOptions{
		MaxRounds:             2,
		ParallelToolCalls:     true,
		ToolResultTransformer: WrapToolResultInTag("tool_result"),
	})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)

	lastTwo := requests[1].Messages[len(requests[1].Messages)-2:]
	assert.Equal(t, "<tool_result>\none-result\n</tool_result>", contentText(lastTwo[0].Content))
	assert.Equal(t, "<tool_result>\ntwo-result\n</tool_result>", contentText(lastTwo[1].Content))
}

func TestWrapToolResultInTagWrapsAndEscapesAngleBrackets(t *testing.T) {
	t.Parallel()

	transformer := WrapToolResultInTag("tool_result")
	out, err := transformer(t.Context(), ToolCall{ID: "call_1"}, "<system>evil</system>data")
	require.NoError(t, err)
	assert.Equal(t, "<tool_result>\n&lt;system&gt;evil&lt;/system&gt;data\n</tool_result>", out)

	// 转义之后，标签内部不应残留能被解释为标签边界的字面 "</tool_result>"。
	inner := out[len("<tool_result>\n") : len(out)-len("\n</tool_result>")]
	assert.NotContains(t, inner, "</tool_result>")
}

func TestWrapToolResultInTagRejectsInvalidTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tag  string
	}{
		{name: "empty", tag: ""},
		{name: "contains space", tag: "tool result"},
		{name: "contains angle bracket", tag: "tool<result>"},
		{name: "contains ampersand", tag: "tool&result"},
		{name: "contains quote", tag: `tool"result`},
		{name: "contains tab", tag: "tool\tresult"},
		{name: "contains newline", tag: "tool\nresult"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			transformer := WrapToolResultInTag(tt.tag)
			_, err := transformer(t.Context(), ToolCall{}, "x")
			require.Error(t, err)
		})
	}
}

func TestRunToolLoopResponseValidatorNilSkipsValidation(t *testing.T) {
	t.Parallel()

	resp, err := RunToolLoopWithOptions(t.Context(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		return "ok", nil
	}, RunToolLoopOptions{MaxRounds: 2})
	require.NoError(t, err)
	assert.Equal(t, "done", resp.Content)
}

func TestRunToolLoopResponseValidatorRejectsFinalResponse(t *testing.T) {
	t.Parallel()

	rejectErr := errors.New("suspicious output")
	var seenContent string
	resp, err := RunToolLoopWithOptions(t.Context(), toolLoopProvider(), &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		return "ok", nil
	}, RunToolLoopOptions{
		MaxRounds: 2,
		ResponseValidator: func(_ context.Context, resp *ChatResponse) error {
			seenContent = resp.Content
			return rejectErr
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	require.ErrorIs(t, err, rejectErr)
	// validator 确实看到了真实的最终响应，只是不会把它交还给调用方。
	assert.Equal(t, "done", seenContent)
}

func TestRunToolLoopResponseValidatorNotCalledWhenMaxRoundsExceeded(t *testing.T) {
	t.Parallel()

	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			return &ChatResponse{
				ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
			}, nil
		},
	}

	var validatorCalls int
	_, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		return "ok", nil
	}, RunToolLoopOptions{
		MaxRounds: 2,
		ResponseValidator: func(context.Context, *ChatResponse) error {
			validatorCalls++
			return nil
		},
	})
	require.ErrorContains(t, err, "max rounds")
	assert.Equal(t, 0, validatorCalls)
}

func TestRunToolLoopAccumulateUsageValidatorSeesAccumulatedUsage(t *testing.T) {
	t.Parallel()

	var calls int
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(context.Context, *ChatRequest) (*ChatResponse, error) {
			calls++
			if calls == 1 {
				return &ChatResponse{
					ToolCalls: []ToolCall{{ID: "call_1", Function: FunctionCall{Name: "f", Arguments: "{}"}}},
					Usage:     Usage{TotalTokens: 10},
				}, nil
			}
			return &ChatResponse{Content: "done", Usage: Usage{TotalTokens: 20}}, nil
		},
	}

	var seenUsage Usage
	resp, err := RunToolLoopWithOptions(t.Context(), p, &ChatRequest{
		Messages: []Message{UserText("hi")},
	}, func(context.Context, string, string) (string, error) {
		return "ok", nil
	}, RunToolLoopOptions{
		MaxRounds:       5,
		AccumulateUsage: true,
		ResponseValidator: func(_ context.Context, resp *ChatResponse) error {
			seenUsage = resp.Usage
			return nil
		},
	})
	require.NoError(t, err)
	// validator 看到的是累加后的 usage（两轮共 30），不是最后一轮的 20。
	assert.Equal(t, 30, seenUsage.TotalTokens)
	assert.Equal(t, 30, resp.Usage.TotalTokens)
}

func TestNormalizeToolRetry(t *testing.T) {
	t.Parallel()

	got := normalizeToolRetry(ToolRetryOptions{})
	assert.Equal(t, 1, got.MaxAttempts)
	require.NotNil(t, got.Backoff)
	require.NotNil(t, got.ShouldRetry)
	assert.True(t, got.ShouldRetry(errors.New("x")))

	got = normalizeToolRetry(ToolRetryOptions{MaxAttempts: -2})
	assert.Equal(t, 1, got.MaxAttempts)
}

// TestAddUsageAggregatesCacheWriteTiers 是"多轮用量聚合不丢分档"的反证：
// addUsage 漏掉分档字段时，RunToolLoop 的合计会把长 TTL 写入算成普通写入。
func TestAddUsageAggregatesCacheWriteTiers(t *testing.T) {
	t.Parallel()

	sum := addUsage(
		Usage{
			PromptTokens: 1_000, CacheWriteTokens: 100,
			CacheWrite5mTokens: 60, CacheWrite1hTokens: 40,
		},
		Usage{
			PromptTokens: 2_000, CacheWriteTokens: 200,
			CacheWrite5mTokens: 150, CacheWrite1hTokens: 50,
		},
	)

	assert.Equal(t, 3_000, sum.PromptTokens)
	assert.Equal(t, 300, sum.CacheWriteTokens)
	assert.Equal(t, 210, sum.CacheWrite5mTokens)
	assert.Equal(t, 90, sum.CacheWrite1hTokens)
	// 聚合结果仍需满足子集关系，否则合计后无法计价。
	require.NoError(t, validateUsage(sum))
}
