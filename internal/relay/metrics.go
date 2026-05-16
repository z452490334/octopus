package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
	"github.com/bestruirui/octopus/internal/utils/log"
)

const defaultRelayLogContentMaxBytes = 256 * 1024

var relayLogContentMaxBytes = envPositiveInt(strings.ToUpper(conf.APP_NAME)+"_RELAY_LOG_CONTENT_MAX_BYTES", defaultRelayLogContentMaxBytes)

const relayLogTruncatedSuffix = "...(truncated)"

// RelayMetrics 负责最终的日志收集与持久化
type RelayMetrics struct {
	APIKeyID     int
	RequestModel string
	StartTime    time.Time

	// 首 Token 时间
	FirstTokenTime time.Time

	// 请求和响应内容
	InternalRequest  *transformerModel.InternalLLMRequest
	InternalResponse *transformerModel.InternalLLMResponse

	// 统计指标
	ActualModel string
	Stats       model.StatsMetrics

	// 参数覆盖
	ParamOverride string
}

func NewRelayMetrics(apiKeyID int, requestModel string, req *transformerModel.InternalLLMRequest) *RelayMetrics {
	return &RelayMetrics{
		APIKeyID:        apiKeyID,
		RequestModel:    requestModel,
		StartTime:       time.Now(),
		InternalRequest: req,
	}
}

func (m *RelayMetrics) SetFirstTokenTime(t time.Time) {
	m.FirstTokenTime = t
}

func (m *RelayMetrics) SetInternalResponse(resp *transformerModel.InternalLLMResponse, actualModel string) {
	m.InternalResponse = resp
	m.ActualModel = actualModel

	if resp == nil || resp.Usage == nil {
		return
	}

	usage := resp.Usage
	m.Stats.InputToken = usage.PromptTokens
	m.Stats.OutputToken = usage.CompletionTokens

	modelPrice := price.GetLLMPrice(actualModel)
	if modelPrice == nil {
		return
	}
	if usage.PromptTokensDetails == nil {
		usage.PromptTokensDetails = &transformerModel.PromptTokensDetails{
			CachedTokens: 0,
		}
	}
	if usage.AnthropicUsage {
		m.Stats.InputCost = (float64(usage.PromptTokensDetails.CachedTokens)*modelPrice.CacheRead +
			float64(usage.PromptTokens)*modelPrice.Input +
			float64(usage.CacheCreationInputTokens)*modelPrice.CacheWrite) * 1e-6
	} else {
		m.Stats.InputCost = (float64(usage.PromptTokensDetails.CachedTokens)*modelPrice.CacheRead + float64(usage.PromptTokens-usage.PromptTokensDetails.CachedTokens)*modelPrice.Input) * 1e-6
	}
	m.Stats.OutputCost = float64(usage.CompletionTokens) * modelPrice.Output * 1e-6
}

func (m *RelayMetrics) Save(ctx context.Context, success bool, err error, attempts []model.ChannelAttempt) {
	duration := time.Since(m.StartTime)

	globalStats := model.StatsMetrics{
		WaitTime:    duration.Milliseconds(),
		InputToken:  m.Stats.InputToken,
		OutputToken: m.Stats.OutputToken,
		InputCost:   m.Stats.InputCost,
		OutputCost:  m.Stats.OutputCost,
	}
	if success {
		globalStats.RequestSuccess = 1
	} else {
		globalStats.RequestFailed = 1
	}

	channelID, channelName := finalChannel(attempts)
	op.StatsTotalUpdate(globalStats)
	op.StatsHourlyUpdate(globalStats)
	op.StatsDailyUpdate(context.Background(), globalStats)
	op.StatsAPIKeyUpdate(m.APIKeyID, globalStats)
	op.StatsChannelUpdate(channelID, globalStats)

	log.Infof("relay complete: model=%s, channel=%d(%s), success=%t, duration=%dms, input_token=%d, output_token=%d, input_cost=%f, output_cost=%f, total_cost=%f, attempts=%d",
		m.RequestModel, channelID, channelName, success, duration.Milliseconds(),
		m.Stats.InputToken, m.Stats.OutputToken,
		m.Stats.InputCost, m.Stats.OutputCost, m.Stats.InputCost+m.Stats.OutputCost,
		len(attempts))

	m.saveLog(ctx, err, duration, attempts, channelID, channelName)
}

func finalChannel(attempts []model.ChannelAttempt) (int, string) {
	var lastID int
	var lastName string
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Status == model.AttemptSuccess {
			return a.ChannelID, a.ChannelName
		}
		if a.Status == model.AttemptFailed && lastID == 0 {
			lastID = a.ChannelID
			lastName = a.ChannelName
		}
	}
	return lastID, lastName
}

func (m *RelayMetrics) saveLog(ctx context.Context, err error, duration time.Duration, attempts []model.ChannelAttempt, channelID int, channelName string) {
	actualModel := m.ActualModel
	if actualModel == "" {
		actualModel = m.RequestModel
	}

	relayLog := model.RelayLog{
		Time:             m.StartTime.Unix(),
		RequestModelName: m.RequestModel,
		ChannelName:      channelName,
		ChannelId:        channelID,
		ActualModelName:  actualModel,
		UseTime:          int(duration.Milliseconds()),
		Attempts:         attempts,
		TotalAttempts:    len(attempts),
	}

	if apiKey, getErr := op.APIKeyGet(m.APIKeyID, ctx); getErr == nil {
		relayLog.RequestAPIKeyName = apiKey.Name
	}

	// 首字时间
	if !m.FirstTokenTime.IsZero() {
		relayLog.Ftut = int(m.FirstTokenTime.Sub(m.StartTime).Milliseconds())
	}

	// Usage
	if m.InternalResponse != nil && m.InternalResponse.Usage != nil {
		relayLog.InputTokens = int(m.InternalResponse.Usage.PromptTokens)
		relayLog.OutputTokens = int(m.InternalResponse.Usage.CompletionTokens)
		relayLog.Cost = m.Stats.InputCost + m.Stats.OutputCost
	}

	// 请求内容
	if m.InternalRequest != nil {
		relayLog.RequestContent = m.requestContentForLog()
	}

	// 响应内容
	if m.InternalResponse != nil {
		respForLog := m.filterResponseForLog(m.InternalResponse)
		if respJSON, jsonErr := json.Marshal(respForLog); jsonErr == nil {
			if m.InternalResponse.Usage != nil && m.InternalResponse.Usage.AnthropicUsage {
				respStr := string(respJSON)
				old := `"usage":{`
				insert := fmt.Sprintf(`"usage":{"cache_creation_input_tokens":%d,`, m.InternalResponse.Usage.CacheCreationInputTokens)
				respJSON = []byte(strings.Replace(respStr, old, insert, 1))
			}
			relayLog.ResponseContent = truncateForRelayLog(string(respJSON))
		}
	}

	// 错误信息
	if err != nil {
		relayLog.Error = err.Error()
	}

	if logErr := op.RelayLogAdd(ctx, relayLog); logErr != nil {
		log.Warnf("failed to save relay log: %v", logErr)
	}
}

func truncateForRelayLog(s string) string {
	if relayLogContentMaxBytes <= 0 || len(s) <= relayLogContentMaxBytes {
		return s
	}
	return s[:relayLogContentMaxBytes] + relayLogTruncatedSuffix
}

func (m *RelayMetrics) requestContentForLog() string {
	if m.InternalRequest == nil {
		return ""
	}

	reqJSON, jsonErr := json.Marshal(m.filterRequestForLog(m.InternalRequest))
	if jsonErr != nil {
		return ""
	}
	if m.ParamOverride == "" {
		return truncateForRelayLog(string(reqJSON))
	}

	var reqMap map[string]any
	if err := json.Unmarshal(reqJSON, &reqMap); err != nil {
		return truncateForRelayLog(string(reqJSON))
	}
	var override map[string]any
	if err := json.Unmarshal([]byte(m.ParamOverride), &override); err != nil {
		return truncateForRelayLog(string(reqJSON))
	}
	maps.Copy(reqMap, override)
	finalJSON, err := json.Marshal(reqMap)
	if err != nil {
		return truncateForRelayLog(string(reqJSON))
	}
	return truncateForRelayLog(string(finalJSON))
}

type relayLogLimiter struct {
	remaining int
	truncated bool
}

func newRelayLogLimiter() *relayLogLimiter {
	return &relayLogLimiter{remaining: relayLogContentMaxBytes}
}

func (l *relayLogLimiter) unlimited() bool {
	return relayLogContentMaxBytes <= 0
}

func (l *relayLogLimiter) exhausted() bool {
	return !l.unlimited() && l.remaining <= 0
}

func (l *relayLogLimiter) reserve(n int) bool {
	if l.unlimited() {
		return true
	}
	if l.remaining <= 0 {
		l.truncated = true
		return false
	}
	if n > l.remaining {
		l.remaining = 0
		l.truncated = true
		return false
	}
	l.remaining -= n
	return true
}

func (l *relayLogLimiter) stringValue(s string) string {
	if l.unlimited() || len(s) <= l.remaining {
		if !l.unlimited() {
			l.remaining -= len(s)
		}
		return s
	}
	l.truncated = true
	if l.remaining <= 0 {
		return relayLogTruncatedSuffix
	}
	n := l.remaining
	l.remaining = 0
	return s[:n] + relayLogTruncatedSuffix
}

func (l *relayLogLimiter) stringPtr(s string) *string {
	v := l.stringValue(s)
	return &v
}

func (l *relayLogLimiter) rawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	if l.unlimited() || len(raw) <= l.remaining {
		if !l.unlimited() {
			l.remaining -= len(raw)
		}
		return raw
	}
	l.truncated = true
	return json.RawMessage(`"[raw JSON omitted for storage]"`)
}

func (m *RelayMetrics) filterRequestForLog(req *transformerModel.InternalLLMRequest) *transformerModel.InternalLLMRequest {
	if req == nil {
		return nil
	}

	limiter := newRelayLogLimiter()
	filtered := *req
	filtered.Messages = filterMessagesForRelayLog(req.Messages, limiter)
	filtered.EmbeddingInput = filterEmbeddingInputForRelayLog(req.EmbeddingInput, limiter)
	filtered.Tools = filterToolsForRelayLog(req.Tools, limiter)
	if req.ResponseFormat != nil {
		format := *req.ResponseFormat
		format.JSONSchema = limiter.rawMessage(req.ResponseFormat.JSONSchema)
		filtered.ResponseFormat = &format
	}
	filtered.Thinking = limiter.rawMessage(req.Thinking)
	filtered.ChatTemplateKwargs = limiter.rawMessage(req.ChatTemplateKwargs)
	filtered.ExtraBody = limiter.rawMessage(req.ExtraBody)
	return &filtered
}

func filterMessagesForRelayLog(messages []transformerModel.Message, limiter *relayLogLimiter) []transformerModel.Message {
	if len(messages) == 0 {
		return nil
	}
	filtered := make([]transformerModel.Message, 0, len(messages))
	for i, msg := range messages {
		if !limiter.reserve(64) {
			filtered = append(filtered, relayLogOmittedMessage(len(messages)-i))
			break
		}
		filtered = append(filtered, filterMessageForRelayLog(msg, limiter))
		if limiter.exhausted() && i+1 < len(messages) {
			filtered = append(filtered, relayLogOmittedMessage(len(messages)-i-1))
			break
		}
	}
	return filtered
}

func filterMessageForRelayLog(msg transformerModel.Message, limiter *relayLogLimiter) transformerModel.Message {
	filtered := msg
	filtered.Content = filterMessageContentForRelayLog(msg.Content, limiter)
	filtered.Images = filterContentPartsForRelayLog(msg.Images, limiter)
	filtered.ToolCalls = filterToolCallsForRelayLog(msg.ToolCalls, limiter)
	if msg.Refusal != "" {
		filtered.Refusal = limiter.stringValue(msg.Refusal)
	}
	if msg.ReasoningContent != nil {
		filtered.ReasoningContent = limiter.stringPtr(*msg.ReasoningContent)
	}
	if msg.Reasoning != nil {
		filtered.Reasoning = limiter.stringPtr(*msg.Reasoning)
	}
	if msg.Audio != nil && msg.Audio.Data != "" {
		audio := *msg.Audio
		audio.Data = "[audio data omitted for storage]"
		filtered.Audio = &audio
	}
	return filtered
}

func filterMessageContentForRelayLog(content transformerModel.MessageContent, limiter *relayLogLimiter) transformerModel.MessageContent {
	filtered := content
	if content.Content != nil {
		filtered.Content = limiter.stringPtr(*content.Content)
	}
	filtered.MultipleContent = filterContentPartsForRelayLog(content.MultipleContent, limiter)
	return filtered
}

func filterContentPartsForRelayLog(parts []transformerModel.MessageContentPart, limiter *relayLogLimiter) []transformerModel.MessageContentPart {
	if len(parts) == 0 {
		return nil
	}
	filtered := make([]transformerModel.MessageContentPart, 0, len(parts))
	for i, part := range parts {
		if !limiter.reserve(32) {
			filtered = append(filtered, relayLogOmittedContentPart(len(parts)-i))
			break
		}
		filtered = append(filtered, filterContentPartForRelayLog(part, limiter))
		if limiter.exhausted() && i+1 < len(parts) {
			filtered = append(filtered, relayLogOmittedContentPart(len(parts)-i-1))
			break
		}
	}
	return filtered
}

func filterContentPartForRelayLog(part transformerModel.MessageContentPart, limiter *relayLogLimiter) transformerModel.MessageContentPart {
	filtered := part
	if part.Text != nil {
		filtered.Text = limiter.stringPtr(*part.Text)
	}
	if part.ImageURL != nil {
		imageURL := *part.ImageURL
		if strings.HasPrefix(imageURL.URL, "data:") || len(imageURL.URL) > 1024 {
			imageURL.URL = "[image data omitted for storage]"
		} else {
			imageURL.URL = limiter.stringValue(imageURL.URL)
		}
		filtered.ImageURL = &imageURL
	}
	if part.Audio != nil {
		audio := *part.Audio
		if audio.Data != "" {
			audio.Data = "[audio data omitted for storage]"
		}
		filtered.Audio = &audio
	}
	if part.File != nil {
		file := *part.File
		if file.FileData != "" {
			file.FileData = "[file data omitted for storage]"
		}
		filtered.File = &file
	}
	return filtered
}

func filterEmbeddingInputForRelayLog(input *transformerModel.EmbeddingInput, limiter *relayLogLimiter) *transformerModel.EmbeddingInput {
	if input == nil {
		return nil
	}
	filtered := *input
	if input.Single != nil {
		filtered.Single = limiter.stringPtr(*input.Single)
	}
	if len(input.Multiple) > 0 {
		filtered.Multiple = make([]string, 0, len(input.Multiple))
		for i, value := range input.Multiple {
			if !limiter.reserve(16) {
				filtered.Multiple = append(filtered.Multiple, fmt.Sprintf("[%d embedding inputs omitted for storage]", len(input.Multiple)-i))
				break
			}
			filtered.Multiple = append(filtered.Multiple, limiter.stringValue(value))
			if limiter.exhausted() && i+1 < len(input.Multiple) {
				filtered.Multiple = append(filtered.Multiple, fmt.Sprintf("[%d embedding inputs omitted for storage]", len(input.Multiple)-i-1))
				break
			}
		}
	}
	return &filtered
}

func filterToolsForRelayLog(tools []transformerModel.Tool, limiter *relayLogLimiter) []transformerModel.Tool {
	if len(tools) == 0 {
		return nil
	}
	filtered := make([]transformerModel.Tool, 0, len(tools))
	for i, tool := range tools {
		if !limiter.reserve(64) {
			break
		}
		copyTool := tool
		copyTool.Function.Parameters = limiter.rawMessage(tool.Function.Parameters)
		filtered = append(filtered, copyTool)
		if limiter.exhausted() && i+1 < len(tools) {
			break
		}
	}
	return filtered
}

func filterToolCallsForRelayLog(toolCalls []transformerModel.ToolCall, limiter *relayLogLimiter) []transformerModel.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	filtered := make([]transformerModel.ToolCall, 0, len(toolCalls))
	for i, toolCall := range toolCalls {
		if !limiter.reserve(32) {
			break
		}
		copyCall := toolCall
		copyCall.Function.Arguments = limiter.stringValue(toolCall.Function.Arguments)
		filtered = append(filtered, copyCall)
		if limiter.exhausted() && i+1 < len(toolCalls) {
			break
		}
	}
	return filtered
}

func relayLogOmittedMessage(count int) transformerModel.Message {
	text := fmt.Sprintf("[%d messages omitted for storage]", count)
	return transformerModel.Message{Content: transformerModel.MessageContent{Content: &text}}
}

func relayLogOmittedContentPart(count int) transformerModel.MessageContentPart {
	text := fmt.Sprintf("[%d content parts omitted for storage]", count)
	return transformerModel.MessageContentPart{Type: "text", Text: &text}
}

// filterResponseForLog 创建响应的浅拷贝，过滤掉 images、MultipleContent 中的图片数据和 Audio.Data 以减少存储压力
func (m *RelayMetrics) filterResponseForLog(resp *transformerModel.InternalLLMResponse) *transformerModel.InternalLLMResponse {
	if resp == nil {
		return nil
	}

	filterMsg := func(msg *transformerModel.Message) *transformerModel.Message {
		if msg == nil {
			return nil
		}
		c := *msg
		c.Images = nil
		if len(c.Content.MultipleContent) > 0 {
			parts := make([]transformerModel.MessageContentPart, 0, len(c.Content.MultipleContent))
			for _, p := range c.Content.MultipleContent {
				if p.Type == "image_url" && p.ImageURL != nil {
					parts = append(parts, transformerModel.MessageContentPart{
						Type:     "image_url",
						ImageURL: &transformerModel.ImageURL{URL: "[image data omitted for storage]"},
					})
				} else {
					parts = append(parts, p)
				}
			}
			c.Content = transformerModel.MessageContent{Content: c.Content.Content, MultipleContent: parts}
		}
		if c.Audio != nil && c.Audio.Data != "" {
			a := *c.Audio
			a.Data = "[audio data omitted for storage]"
			c.Audio = &a
		}
		return &c
	}

	filtered := *resp
	filtered.Choices = make([]transformerModel.Choice, len(resp.Choices))
	for i, choice := range resp.Choices {
		filtered.Choices[i] = choice
		filtered.Choices[i].Message = filterMsg(choice.Message)
		filtered.Choices[i].Delta = filterMsg(choice.Delta)
	}
	return &filtered
}
