package geminichat

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiResponseToolCallKeepsThoughtSignature(t *testing.T) {
	signature := []byte(`"sig_real_123"`)
	response := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			Content: dto.GeminiChatContent{
				Role: "model",
				Parts: []dto.GeminiPart{
					{Text: "checking", ThoughtSignature: signature},
					{
						FunctionCall:     &dto.FunctionCall{ID: "call_1", FunctionName: "lookup", Arguments: map[string]any{"q": "x"}},
						ThoughtSignature: signature,
					},
					{
						FunctionCall:     &dto.FunctionCall{FunctionName: "lookup", Arguments: map[string]any{"q": "y"}},
						ThoughtSignature: []byte(`"sig_real_456"`),
					},
				},
			},
		}},
	}

	got := ResponseGeminiChat2OpenAI("chatcmpl-1", 1, response)

	require.Len(t, got.Choices, 1)
	calls := got.Choices[0].Message.ParseToolCalls()
	require.Len(t, calls, 2)
	assert.JSONEq(t, `{"google":{"thought_signature":"sig_real_123"}}`, string(calls[0].ExtraContent))
	assert.JSONEq(t, `{"google":{"thought_signature":"sig_real_456"}}`, string(calls[1].ExtraContent))
	assert.NotEmpty(t, calls[1].ID)

	stream, isStop := StreamResponseGeminiChat2OpenAI(response)
	require.False(t, isStop)
	require.Len(t, stream.Choices[0].Delta.ToolCalls, 2)
	assert.JSONEq(t, `{"google":{"thought_signature":"sig_real_123"}}`, string(stream.Choices[0].Delta.ToolCalls[0].ExtraContent))
	assert.JSONEq(t, `{"google":{"thought_signature":"sig_real_456"}}`, string(stream.Choices[0].Delta.ToolCalls[1].ExtraContent))
}

func TestOpenAIFinishReasonFromGeminiDoesNotTreatUnknownAsContentFilter(t *testing.T) {
	cases := []struct {
		reason string
		want   string
		ok     bool
	}{
		{reason: "STOP", want: types.FinishReasonStop, ok: true},
		{reason: "MAX_TOKENS", want: types.FinishReasonLength, ok: true},
		{reason: "SAFETY", want: types.FinishReasonContentFilter, ok: true},
		{reason: "PROHIBITED_CONTENT", want: types.FinishReasonContentFilter, ok: true},
		{reason: "FINISH_REASON_UNSPECIFIED", ok: false},
		{reason: "OTHER", ok: false},
		{reason: "", ok: false},
	}
	for _, tc := range cases {
		got, ok := openAIFinishReasonFromGemini(tc.reason)
		assert.Equal(t, tc.ok, ok, tc.reason)
		assert.Equal(t, tc.want, got, tc.reason)
	}

	reason := "FINISH_REASON_UNSPECIFIED"
	response := &dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{{
			FinishReason: &reason,
			Content:      dto.GeminiChatContent{Role: "model"},
		}},
	}
	got := ResponseGeminiChat2OpenAI("chatcmpl-1", 1, response)
	assert.Equal(t, types.FinishReasonStop, got.Choices[0].FinishReason)

	stream, _ := StreamResponseGeminiChat2OpenAI(response)
	assert.Nil(t, stream.Choices[0].FinishReason)
}
