package streamagg

import (
	"testing"

	"github.com/bestruirui/octopus/internal/transformer/model"
)

func TestAggregatorBuildsResponseWithoutKeepingChunks(t *testing.T) {
	agg := New(ToolCallNameAppend)
	agg.Add(&model.InternalLLMResponse{
		ID:      "chunk-1",
		Object:  "chat.completion.chunk",
		Created: 123,
		Model:   "test-model",
		Choices: []model.Choice{
			{
				Index: 0,
				Delta: &model.Message{
					Role: "assistant",
					Content: model.MessageContent{
						Content: ptr("hel"),
					},
					ToolCalls: []model.ToolCall{
						{
							Index: 0,
							ID:    "call_1",
							Type:  "function",
							Function: model.FunctionCall{
								Name:      "get",
								Arguments: `{"q":`,
							},
						},
					},
				},
			},
		},
	})

	stop := "tool_calls"
	agg.Add(&model.InternalLLMResponse{
		ID:     "chunk-2",
		Object: "chat.completion.chunk",
		Model:  "actual-model",
		Usage: &model.Usage{
			PromptTokens:     10,
			CompletionTokens: 4,
			TotalTokens:      14,
		},
		Choices: []model.Choice{
			{
				Index: 0,
				Delta: &model.Message{
					Content: model.MessageContent{
						Content: ptr("lo"),
					},
					ReasoningContent: ptr("why"),
					ToolCalls: []model.ToolCall{
						{
							Index: 0,
							Function: model.FunctionCall{
								Name:      "_weather",
								Arguments: `"x"}`,
							},
						},
					},
				},
				FinishReason: &stop,
			},
		},
	})

	resp := agg.Response()
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.ID != "chunk-2" || resp.Object != "chat.completion" || resp.Model != "actual-model" {
		t.Fatalf("unexpected response metadata: %+v", resp)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 14 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg == nil {
		t.Fatal("expected aggregated message")
	}
	if got := *msg.Content.Content; got != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
	if got := *msg.ReasoningContent; got != "why" {
		t.Fatalf("reasoning = %q, want why", got)
	}
	if got := msg.ToolCalls[0].Function.Name; got != "get_weather" {
		t.Fatalf("tool name = %q, want get_weather", got)
	}
	if got := msg.ToolCalls[0].Function.Arguments; got != `{"q":"x"}` {
		t.Fatalf("tool args = %q, want JSON args", got)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("unexpected finish reason: %+v", resp.Choices[0].FinishReason)
	}
	if agg.Response() != nil {
		t.Fatal("expected aggregator to release response after Response")
	}
}

func TestAggregatorCanReplaceToolCallName(t *testing.T) {
	agg := New(ToolCallNameReplace)
	agg.Add(&model.InternalLLMResponse{
		Object: "chat.completion.chunk",
		Choices: []model.Choice{
			{Index: 0, Delta: &model.Message{ToolCalls: []model.ToolCall{{Index: 0, Function: model.FunctionCall{Name: "first"}}}}},
			{Index: 0, Delta: &model.Message{ToolCalls: []model.ToolCall{{Index: 0, Function: model.FunctionCall{Name: "second"}}}}},
		},
	})

	resp := agg.Response()
	if resp.Choices[0].Message.ToolCalls[0].Function.Name != "second" {
		t.Fatalf("tool name = %q, want second", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	}
}

func ptr(s string) *string {
	return &s
}
