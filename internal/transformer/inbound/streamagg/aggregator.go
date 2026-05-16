package streamagg

import (
	"os"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/transformer/model"
)

const defaultMaxAggregatedFieldBytes = 4 * 1024 * 1024

var maxAggregatedFieldBytes = envPositiveInt(strings.ToUpper(conf.APP_NAME)+"_STREAM_AGG_MAX_FIELD_BYTES", defaultMaxAggregatedFieldBytes)

const truncatedSuffix = "...(truncated)"

type ToolCallNameMergeMode int

const (
	ToolCallNameAppend ToolCallNameMergeMode = iota
	ToolCallNameReplace
)

type Aggregator struct {
	result            *model.InternalLLMResponse
	choices           map[int]*model.Choice
	toolCallNameMerge ToolCallNameMergeMode
}

func New(toolCallNameMerge ToolCallNameMergeMode) *Aggregator {
	return &Aggregator{toolCallNameMerge: toolCallNameMerge}
}

func (a *Aggregator) Add(chunk *model.InternalLLMResponse) {
	if chunk == nil || chunk.Object == "[DONE]" {
		return
	}
	if a.result == nil {
		a.result = &model.InternalLLMResponse{
			ID:                chunk.ID,
			Object:            "chat.completion",
			Created:           chunk.Created,
			Model:             chunk.Model,
			SystemFingerprint: chunk.SystemFingerprint,
			ServiceTier:       chunk.ServiceTier,
		}
		a.choices = make(map[int]*model.Choice)
	}

	if chunk.ID != "" {
		a.result.ID = chunk.ID
	}
	if chunk.Model != "" {
		a.result.Model = chunk.Model
	}
	if chunk.Usage != nil {
		a.result.Usage = chunk.Usage
	}

	for _, choice := range chunk.Choices {
		existing := a.choice(choice.Index)
		if choice.Delta != nil {
			a.mergeDelta(existing.Message, choice.Delta)
		}
		if choice.FinishReason != nil {
			existing.FinishReason = choice.FinishReason
		}
		if choice.Logprobs != nil {
			if existing.Logprobs == nil {
				existing.Logprobs = &model.LogprobsContent{}
			}
			existing.Logprobs.Content = append(existing.Logprobs.Content, choice.Logprobs.Content...)
		}
	}
}

func (a *Aggregator) Response() *model.InternalLLMResponse {
	if a.result == nil {
		return nil
	}
	a.result.Choices = make([]model.Choice, 0, len(a.choices))
	for idx := 0; idx < len(a.choices); idx++ {
		if choice, ok := a.choices[idx]; ok {
			a.result.Choices = append(a.result.Choices, *choice)
		}
	}
	result := a.result
	a.result = nil
	a.choices = nil
	return result
}

func (a *Aggregator) choice(index int) *model.Choice {
	existing, ok := a.choices[index]
	if ok {
		return existing
	}
	existing = &model.Choice{
		Index:   index,
		Message: &model.Message{},
	}
	a.choices[index] = existing
	return existing
}

func (a *Aggregator) mergeDelta(dst *model.Message, delta *model.Message) {
	if delta.Role != "" {
		dst.Role = delta.Role
	}

	if delta.Content.Content != nil {
		if dst.Content.Content == nil {
			dst.Content.Content = new(string)
		}
		*dst.Content.Content = appendLimited(*dst.Content.Content, *delta.Content.Content)
	}

	if len(delta.Content.MultipleContent) > 0 {
		dst.Content.MultipleContent = append(dst.Content.MultipleContent, delta.Content.MultipleContent...)
	}
	if len(delta.Images) > 0 {
		dst.Content.MultipleContent = append(dst.Content.MultipleContent, delta.Images...)
	}

	if reasoning := delta.GetReasoningContent(); reasoning != "" {
		if dst.ReasoningContent == nil {
			dst.ReasoningContent = new(string)
		}
		*dst.ReasoningContent = appendLimited(*dst.ReasoningContent, reasoning)
	}

	for _, toolCall := range delta.ToolCalls {
		dst.ToolCalls = a.mergeToolCall(dst.ToolCalls, toolCall)
	}

	if delta.Refusal != "" {
		dst.Refusal = delta.Refusal
	}
}

func (a *Aggregator) mergeToolCall(toolCalls []model.ToolCall, delta model.ToolCall) []model.ToolCall {
	for i, tc := range toolCalls {
		if tc.Index == delta.Index {
			if delta.ID != "" {
				toolCalls[i].ID = delta.ID
			}
			if delta.Type != "" {
				toolCalls[i].Type = delta.Type
			}
			if delta.Function.Name != "" {
				if a.toolCallNameMerge == ToolCallNameReplace {
					toolCalls[i].Function.Name = delta.Function.Name
				} else {
					toolCalls[i].Function.Name += delta.Function.Name
				}
			}
			if delta.Function.Arguments != "" {
				toolCalls[i].Function.Arguments = appendLimited(toolCalls[i].Function.Arguments, delta.Function.Arguments)
			}
			return toolCalls
		}
	}
	return append(toolCalls, delta)
}

func appendLimited(dst, add string) string {
	if maxAggregatedFieldBytes <= 0 || add == "" || strings.HasSuffix(dst, truncatedSuffix) {
		return dst
	}
	if len(dst)+len(add) <= maxAggregatedFieldBytes {
		return dst + add
	}
	keep := maxAggregatedFieldBytes - len(dst)
	if keep < 0 {
		keep = 0
	}
	if keep > len(add) {
		keep = len(add)
	}
	return dst + add[:keep] + truncatedSuffix
}

func envPositiveInt(name string, def int) int {
	if raw := strings.TrimSpace(os.Getenv(name)); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			return v
		}
	}
	return def
}
