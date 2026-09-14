package xai

import "github.com/QuantumNous/new-api/relaykit/dto"

// ChatCompletionResponse represents the response from XAI chat completion API
type ChatCompletionResponse struct {
	Id                string                         `json:"id"`
	Object            string                         `json:"object"`
	Created           int64                          `json:"created"`
	Model             string                         `json:"model"`
	Choices           []dto.OpenAITextResponseChoice `json:"choices"`
	Usage             *dto.Usage                     `json:"usage"`
	SystemFingerprint string                         `json:"system_fingerprint"`
}

type ImageInput struct {
	Type   string `json:"type,omitempty"`
	URL    string `json:"url,omitempty"`
	FileID string `json:"file_id,omitempty"`
}

// quality, size or style are not supported by xAI API at the moment.
type ImageRequest struct {
	Model  string       `json:"model"`
	Prompt string       `json:"prompt" binding:"required"`
	N      *uint        `json:"n,omitempty"`
	Image  *ImageInput  `json:"image,omitempty"`
	Images []ImageInput `json:"images,omitempty"`
	// Size           string          `json:"size,omitempty"`
	// Quality        string          `json:"quality,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
	// Style          string          `json:"style,omitempty"`
	// User           string          `json:"user,omitempty"`
	// ExtraFields    json.RawMessage `json:"extra_fields,omitempty"`
}
