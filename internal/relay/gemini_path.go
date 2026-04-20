package relay

import "strings"

// ParseGeminiPathForRequest is the exported alias used by HTTP handlers to validate a Gemini route before relaying.
func ParseGeminiPathForRequest(path string) (modelName string, stream bool, ok bool) {
	return parseGeminiPath(path)
}

// parseGeminiPath extracts the model resource id and streaming mode from a Google-style path such as:
// /v1beta/models/gemini-2.0-flash:generateContent
// /v1beta/models/gemini-2.0-flash:streamGenerateContent
func parseGeminiPath(path string) (modelName string, stream bool, ok bool) {
	const needle = "/models/"
	i := strings.Index(path, needle)
	if i < 0 {
		return "", false, false
	}
	rest := strings.Trim(strings.TrimPrefix(path[i:], needle), "/")
	if rest == "" {
		return "", false, false
	}
	colon := strings.LastIndex(rest, ":")
	if colon < 0 {
		return "", false, false
	}
	modelName = rest[:colon]
	method := rest[colon+1:]
	switch method {
	case "generateContent":
		return modelName, false, true
	case "streamGenerateContent":
		return modelName, true, true
	default:
		return "", false, false
	}
}
