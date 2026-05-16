package relay

import (
	"strings"
	"testing"

	transformerModel "github.com/bestruirui/octopus/internal/transformer/model"
)

func TestRequestContentForLogTruncatesBeforeMarshal(t *testing.T) {
	oldMax := relayLogContentMaxBytes
	relayLogContentMaxBytes = 128
	t.Cleanup(func() { relayLogContentMaxBytes = oldMax })

	largeText := strings.Repeat("a", 4096)
	metrics := &RelayMetrics{
		InternalRequest: &transformerModel.InternalLLMRequest{
			Model: "gpt-test",
			Messages: []transformerModel.Message{
				{
					Role: "user",
					Content: transformerModel.MessageContent{
						Content: &largeText,
					},
				},
			},
		},
	}

	content := metrics.requestContentForLog()
	if len(content) > relayLogContentMaxBytes+len(relayLogTruncatedSuffix) {
		t.Fatalf("log content length = %d, want <= %d", len(content), relayLogContentMaxBytes+len(relayLogTruncatedSuffix))
	}
	if strings.Contains(content, strings.Repeat("a", 512)) {
		t.Fatalf("log content contains an unbounded copy of the original text")
	}
	if !strings.Contains(content, relayLogTruncatedSuffix) {
		t.Fatalf("log content should be marked as truncated: %q", content)
	}
}

func TestRequestContentForLogOmitsBinaryPayloads(t *testing.T) {
	oldMax := relayLogContentMaxBytes
	relayLogContentMaxBytes = 1024
	t.Cleanup(func() { relayLogContentMaxBytes = oldMax })

	imageURL := "data:image/png;base64," + strings.Repeat("a", 2048)
	audioData := strings.Repeat("b", 2048)
	fileData := strings.Repeat("c", 2048)
	metrics := &RelayMetrics{
		InternalRequest: &transformerModel.InternalLLMRequest{
			Model: "gpt-test",
			Messages: []transformerModel.Message{
				{
					Role: "user",
					Content: transformerModel.MessageContent{MultipleContent: []transformerModel.MessageContentPart{
						{Type: "image_url", ImageURL: &transformerModel.ImageURL{URL: imageURL}},
						{Type: "input_audio", Audio: &transformerModel.Audio{Format: "wav", Data: audioData}},
						{Type: "file", File: &transformerModel.File{Filename: "large.txt", FileData: fileData}},
					}},
				},
			},
		},
	}

	content := metrics.requestContentForLog()
	for _, raw := range []string{imageURL, audioData, fileData} {
		if strings.Contains(content, raw) {
			t.Fatalf("log content contains raw binary payload")
		}
	}
	for _, marker := range []string{"[image data omitted for storage]", "[audio data omitted for storage]", "[file data omitted for storage]"} {
		if !strings.Contains(content, marker) {
			t.Fatalf("log content missing marker %q: %s", marker, content)
		}
	}
}
