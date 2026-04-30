package relay

import (
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/transformer/model"
)

// normalizeResponseShape ensures OpenAI-compatible fields always exist.
func normalizeResponseShape(resp *model.InternalLLMResponse, fallbackModel string) {
	if resp == nil {
		return
	}

	if resp.ID == "" {
		resp.ID = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	if resp.Object == "" {
		resp.Object = "chat.completion"
	}
	if resp.Created == 0 {
		resp.Created = time.Now().Unix()
	}
	if resp.Model == "" {
		resp.Model = fallbackModel
	}

	if resp.Usage == nil {
		resp.Usage = &model.Usage{}
	}
	if resp.Usage.TotalTokens == 0 {
		resp.Usage.TotalTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens
	}
	if resp.Usage.PromptTokensDetails == nil {
		resp.Usage.PromptTokensDetails = &model.PromptTokensDetails{}
	}
	if resp.Usage.CompletionTokensDetails == nil {
		resp.Usage.CompletionTokensDetails = &model.CompletionTokensDetails{}
	}
}
