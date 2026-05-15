package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bestruirui/octopus/internal/transformer/inbound/streamagg"
	"github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/xurl"
)

// MessagesInbound adapts Google Gemini generateContent / streamGenerateContent to the internal model
// and converts internal responses back to Gemini JSON (so any upstream channel can be exposed as Gemini).
type MessagesInbound struct {
	streamAgg      *streamagg.Aggregator
	storedResponse *model.InternalLLMResponse
}

func (i *MessagesInbound) TransformRequest(ctx context.Context, body []byte) (*model.InternalLLMRequest, error) {
	var geminiReq model.GeminiGenerateContentRequest
	if err := json.Unmarshal(body, &geminiReq); err != nil {
		return nil, err
	}
	req, err := geminiGenerateContentToInternal(&geminiReq)
	if err != nil {
		return nil, err
	}
	req.RawRequest = body
	req.RawAPIFormat = model.APIFormatGeminiContents
	return req, nil
}

func (i *MessagesInbound) TransformResponse(ctx context.Context, response *model.InternalLLMResponse) ([]byte, error) {
	i.storedResponse = response
	gem := internalToGeminiGenerateContentResponse(response)
	return json.Marshal(gem)
}

func (i *MessagesInbound) TransformStream(ctx context.Context, stream *model.InternalLLMResponse) ([]byte, error) {
	if stream.Object == "[DONE]" {
		return nil, nil
	}
	i.aggregateStream(stream)
	gem := internalToGeminiGenerateContentResponse(stream)
	body, err := json.Marshal(gem)
	if err != nil {
		return nil, err
	}
	return []byte("data: " + string(body) + "\n\n"), nil
}

func (i *MessagesInbound) GetInternalResponse(ctx context.Context) (*model.InternalLLMResponse, error) {
	if i.storedResponse != nil {
		return i.storedResponse, nil
	}
	if i.streamAgg == nil {
		return nil, nil
	}
	return i.streamAgg.Response(), nil
}

func (i *MessagesInbound) aggregateStream(stream *model.InternalLLMResponse) {
	if i.streamAgg == nil {
		i.streamAgg = streamagg.New(streamagg.ToolCallNameReplace)
	}
	i.streamAgg.Add(stream)
}

func geminiGenerateContentToInternal(g *model.GeminiGenerateContentRequest) (*model.InternalLLMRequest, error) {
	req := &model.InternalLLMRequest{
		Messages:            []model.Message{},
		TransformerMetadata: map[string]string{},
	}
	if g.SystemInstruction != nil {
		for _, p := range g.SystemInstruction.Parts {
			if p == nil {
				continue
			}
			if p.Text != "" {
				t := p.Text
				req.Messages = append(req.Messages, model.Message{
					Role:    "system",
					Content: model.MessageContent{Content: &t},
				})
			}
		}
	}
	for _, c := range g.Contents {
		if c == nil {
			continue
		}
		role := strings.TrimSpace(c.Role)
		switch role {
		case "user", "":
			if err := appendGeminiUserContent(req, c); err != nil {
				return nil, err
			}
		case "model":
			if err := appendGeminiModelContent(req, c); err != nil {
				return nil, err
			}
		default:
			if err := appendGeminiUserContent(req, c); err != nil {
				return nil, err
			}
		}
	}
	if g.GenerationConfig != nil {
		gc := g.GenerationConfig
		if gc.MaxOutputTokens > 0 {
			m := int64(gc.MaxOutputTokens)
			req.MaxTokens = &m
		}
		req.Temperature = gc.Temperature
		req.TopP = gc.TopP
		if gc.TopK != nil {
			req.TransformerMetadata["gemini_top_k"] = fmt.Sprintf("%d", *gc.TopK)
		}
		if len(gc.StopSequences) > 0 {
			req.Stop = &model.Stop{MultipleStop: gc.StopSequences}
		}
		if gc.ResponseMimeType != "" {
			switch gc.ResponseMimeType {
			case "application/json":
				req.ResponseFormat = &model.ResponseFormat{Type: "json_object"}
			case "text/plain":
				req.ResponseFormat = &model.ResponseFormat{Type: "text"}
			default:
				req.ResponseFormat = &model.ResponseFormat{Type: "text"}
			}
		}
		if len(gc.ResponseModalities) > 0 {
			for _, m := range gc.ResponseModalities {
				if m == "" {
					continue
				}
				req.Modalities = append(req.Modalities, strings.ToLower(m))
			}
		}
		if gc.ThinkingConfig != nil {
			if gc.ThinkingConfig.ThinkingBudget != nil && *gc.ThinkingConfig.ThinkingBudget > 0 {
				switch {
				case *gc.ThinkingConfig.ThinkingBudget <= 1024:
					req.ReasoningEffort = "low"
				case *gc.ThinkingConfig.ThinkingBudget <= 4096:
					req.ReasoningEffort = "medium"
				default:
					req.ReasoningEffort = "high"
				}
			}
		}
	}
	if len(g.SafetySettings) > 0 {
		if b, err := json.Marshal(g.SafetySettings); err == nil {
			req.TransformerMetadata["gemini_safety_settings"] = string(b)
		}
	}
	if len(g.Tools) > 0 {
		for _, tool := range g.Tools {
			if tool == nil {
				continue
			}
			for _, decl := range tool.FunctionDeclarations {
				if decl == nil {
					continue
				}
				params := json.RawMessage(nil)
				if decl.Parameters != nil {
					params, _ = json.Marshal(decl.Parameters)
				}
				req.Tools = append(req.Tools, model.Tool{
					Type: "function",
					Function: model.Function{
						Name:        decl.Name,
						Description: decl.Description,
						Parameters:  params,
					},
				})
			}
		}
	}
	if g.ToolConfig != nil && g.ToolConfig.FunctionCallingConfig != nil {
		fc := g.ToolConfig.FunctionCallingConfig
		mode := strings.ToUpper(strings.TrimSpace(fc.Mode))
		switch mode {
		case "NONE":
			s := "none"
			req.ToolChoice = &model.ToolChoice{ToolChoice: &s}
		case "ANY":
			if len(fc.AllowedFunctionNames) == 1 {
				req.ToolChoice = &model.ToolChoice{
					NamedToolChoice: &model.NamedToolChoice{
						Type:     "function",
						Function: model.ToolFunction{Name: fc.AllowedFunctionNames[0]},
					},
				}
			} else {
				s := "required"
				req.ToolChoice = &model.ToolChoice{ToolChoice: &s}
			}
		case "AUTO", "":
			s := "auto"
			req.ToolChoice = &model.ToolChoice{ToolChoice: &s}
		default:
			s := "auto"
			req.ToolChoice = &model.ToolChoice{ToolChoice: &s}
		}
	}
	return req, nil
}

func appendGeminiUserContent(req *model.InternalLLMRequest, c *model.GeminiContent) error {
	var textBuf strings.Builder
	var parts []model.MessageContentPart
	var toolMsgs []model.Message
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		if p.FunctionResponse != nil {
			fr := p.FunctionResponse
			b, _ := json.Marshal(fr.Response)
			s := string(b)
			id := fr.Name
			toolMsgs = append(toolMsgs, model.Message{
				Role:       "tool",
				ToolCallID: &id,
				Content:    model.MessageContent{Content: &s},
			})
			continue
		}
		if p.Text != "" {
			textBuf.WriteString(p.Text)
		}
		if p.InlineData != nil {
			url := fmt.Sprintf("data:%s;base64,%s", p.InlineData.MimeType, p.InlineData.Data)
			parts = append(parts, model.MessageContentPart{
				Type: "image_url",
				ImageURL: &model.ImageURL{
					URL: url,
				},
			})
		}
		if p.FileData != nil && xurl.IsDataURL(p.FileData.FileURI) {
			parts = append(parts, model.MessageContentPart{
				Type: "file",
				File: &model.File{Filename: "upload", FileData: p.FileData.FileURI},
			})
		}
	}
	if textBuf.Len() > 0 {
		t := textBuf.String()
		if len(parts) > 0 {
			parts = append([]model.MessageContentPart{{Type: "text", Text: &t}}, parts...)
		} else {
			req.Messages = append(req.Messages, model.Message{
				Role:    "user",
				Content: model.MessageContent{Content: &t},
			})
		}
	}
	if len(parts) > 0 {
		req.Messages = append(req.Messages, model.Message{
			Role:    "user",
			Content: model.MessageContent{MultipleContent: parts},
		})
	}
	req.Messages = append(req.Messages, toolMsgs...)
	return nil
}

func appendGeminiModelContent(req *model.InternalLLMRequest, c *model.GeminiContent) error {
	msg := model.Message{Role: "assistant"}
	var texts []string
	var reasoning []string
	var toolCalls []model.ToolCall
	idx := 0
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		if p.FunctionCall != nil {
			args, _ := json.Marshal(p.FunctionCall.Args)
			id := fmt.Sprintf("call_%s_%d", p.FunctionCall.Name, idx)
			idx++
			toolCalls = append(toolCalls, model.ToolCall{
				ID:    id,
				Type:  "function",
				Index: len(toolCalls),
				Function: model.FunctionCall{
					Name:      p.FunctionCall.Name,
					Arguments: string(args),
				},
			})
			continue
		}
		if p.Thought && p.Text != "" {
			reasoning = append(reasoning, p.Text)
			continue
		}
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	if len(reasoning) > 0 {
		r := strings.Join(reasoning, "")
		msg.ReasoningContent = &r
	}
	if len(texts) > 0 {
		t := strings.Join(texts, "")
		msg.Content = model.MessageContent{Content: &t}
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	if msg.Content.Content != nil || len(msg.ToolCalls) > 0 || msg.ReasoningContent != nil {
		req.Messages = append(req.Messages, msg)
	}
	return nil
}

func internalToGeminiGenerateContentResponse(resp *model.InternalLLMResponse) *model.GeminiGenerateContentResponse {
	out := &model.GeminiGenerateContentResponse{}
	isStream := resp.Object == "chat.completion.chunk"
	for _, ch := range resp.Choices {
		cand := internalChoiceToGeminiCandidate(&ch, isStream)
		if cand != nil {
			out.Candidates = append(out.Candidates, cand)
		}
	}
	out.UsageMetadata = usageToGeminiUsage(resp.Usage)
	if resp.Model != "" {
		out.ModelVersion = resp.Model
	}
	return out
}

func internalChoiceToGeminiCandidate(ch *model.Choice, isStream bool) *model.GeminiCandidate {
	cand := &model.GeminiCandidate{Index: ch.Index}
	var msg *model.Message
	if isStream {
		msg = ch.Delta
	} else {
		msg = ch.Message
	}
	if ch.FinishReason != nil {
		cand.FinishReason = internalFinishReasonToGemini(ch.FinishReason)
	}
	if msg == nil {
		return cand
	}
	parts := messageToGeminiParts(msg)
	if len(parts) == 0 {
		return cand
	}
	cand.Content = &model.GeminiContent{
		Role:  "model",
		Parts: parts,
	}
	return cand
}

func messageToGeminiParts(msg *model.Message) []*model.GeminiPart {
	var parts []*model.GeminiPart
	if rc := msg.GetReasoningContent(); rc != "" {
		parts = append(parts, &model.GeminiPart{Thought: true, Text: rc})
	}
	if msg.Content.Content != nil && *msg.Content.Content != "" {
		parts = append(parts, &model.GeminiPart{Text: *msg.Content.Content})
	}
	for _, mc := range msg.Content.MultipleContent {
		switch mc.Type {
		case "text":
			if mc.Text != nil {
				parts = append(parts, &model.GeminiPart{Text: *mc.Text})
			}
		case "image_url":
			if mc.ImageURL != nil {
				du := xurl.ParseDataURL(mc.ImageURL.URL)
				if du != nil && du.IsBase64 {
					parts = append(parts, &model.GeminiPart{
						InlineData: &model.GeminiBlob{MimeType: du.MediaType, Data: du.Data},
					})
				}
			}
		case "input_audio":
			if mc.Audio != nil {
				parts = append(parts, &model.GeminiPart{
					InlineData: &model.GeminiBlob{MimeType: audioFormatToMime(mc.Audio.Format), Data: mc.Audio.Data},
				})
			}
		case "file":
			if mc.File != nil {
				du := xurl.ParseDataURL(mc.File.FileData)
				if du != nil && du.IsBase64 {
					parts = append(parts, &model.GeminiPart{
						InlineData: &model.GeminiBlob{MimeType: du.MediaType, Data: du.Data},
					})
				}
			}
		}
	}
	for _, tc := range msg.ToolCalls {
		var args map[string]interface{}
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		parts = append(parts, &model.GeminiPart{
			FunctionCall: &model.GeminiFunctionCall{
				Name: tc.Function.Name,
				Args: args,
			},
			ThoughtSignature: "skip_thought_signature_validator",
		})
	}
	return parts
}

func audioFormatToMime(format string) string {
	switch strings.ToLower(format) {
	case "wav":
		return "audio/wav"
	case "mp3":
		return "audio/mp3"
	case "aiff":
		return "audio/aiff"
	case "aac":
		return "audio/aac"
	case "ogg":
		return "audio/ogg"
	case "flac":
		return "audio/flac"
	default:
		return "audio/wav"
	}
}

func internalFinishReasonToGemini(fr *string) *string {
	if fr == nil {
		return nil
	}
	switch *fr {
	case "length":
		s := "MAX_TOKENS"
		return &s
	case "content_filter":
		s := "SAFETY"
		return &s
	case "stop", "tool_calls", "function_call":
		s := "STOP"
		return &s
	default:
		s := "STOP"
		return &s
	}
}

func usageToGeminiUsage(u *model.Usage) *model.GeminiUsageMetadata {
	if u == nil {
		return nil
	}
	meta := &model.GeminiUsageMetadata{
		PromptTokenCount:     int(u.PromptTokens),
		CandidatesTokenCount: int(u.CompletionTokens),
		TotalTokenCount:      int(u.TotalTokens),
	}
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		meta.CachedContentTokenCount = int(u.PromptTokensDetails.CachedTokens)
	}
	if u.CompletionTokensDetails != nil && u.CompletionTokensDetails.ReasoningTokens > 0 {
		meta.ThoughtsTokenCount = int(u.CompletionTokensDetails.ReasoningTokens)
	}
	return meta
}
