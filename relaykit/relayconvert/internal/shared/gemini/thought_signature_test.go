package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert/convmeta"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThoughtSignatureRoundTrip(t *testing.T) {
	extra := OpenAIThoughtSignature(ThoughtSignatureJSON([]byte(`"sig_real_123"`)))
	assert.JSONEq(t, `{"google":{"thought_signature":"sig_real_123"}}`, string(extra))
	assert.Equal(t, `"sig_real_123"`, ThoughtSignatureFromOpenAIToolCall(extra, nil))

	unquoted := ThoughtSignatureFromOpenAIToolCall([]byte(`{"google":{"thought_signature":"sig_real_123"}}`), nil)
	assert.Equal(t, `"sig_real_123"`, unquoted)

	assert.Empty(t, ThoughtSignatureFromOpenAIToolCall(nil, nil))
	assert.Empty(t, ThoughtSignatureFromOpenAIToolCall([]byte(`{"google":{}}`), nil))
	assert.Nil(t, OpenAIThoughtSignature(""))
	assert.Empty(t, ThoughtSignatureJSON([]byte("not json")))
}

func TestApplyThoughtSignatureToFunctionCall(t *testing.T) {
	enabled := &convmeta.Options{Gemini: convmeta.GeminiOptions{FunctionCallThoughtSignatureEnabled: true}}

	withReal := functionCallPart()
	require.True(t, ApplyThoughtSignatureToFunctionCall(enabled, &withReal, `"sig_real_123"`))
	assert.Equal(t, `"sig_real_123"`, string(withReal.ThoughtSignature))

	missing := functionCallPart()
	require.True(t, ApplyThoughtSignatureToFunctionCall(enabled, &missing, ""))
	assert.Equal(t, `"context_engineering_is_the_way_to_go"`, string(missing.ThoughtSignature))

	disabled := functionCallPart()
	assert.False(t, ApplyThoughtSignatureToFunctionCall(nil, &disabled, ""))
	assert.Empty(t, disabled.ThoughtSignature)

	kept := functionCallPart()
	require.True(t, ApplyThoughtSignatureToFunctionCall(nil, &kept, `"sig_real_123"`))
	assert.Equal(t, `"sig_real_123"`, string(kept.ThoughtSignature))
}

func functionCallPart() dto.GeminiPart {
	return dto.GeminiPart{FunctionCall: &dto.FunctionCall{FunctionName: "lookup"}}
}
