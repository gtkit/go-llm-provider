package provider

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSONObjectFormat(t *testing.T) {
	t.Parallel()

	format := JSONObjectFormat()

	require.NotNil(t, format)
	assert.Equal(t, ResponseFormatJSONObject, format.Type)
	assert.Nil(t, format.Schema)
	assert.Empty(t, format.Name)
	assert.Nil(t, format.Strict)
}

func TestJSONSchemaFormatStrict(t *testing.T) {
	t.Parallel()

	format := JSONSchemaFormatStrict("city_response", ParamSchema{
		Type: "object",
		Properties: map[string]ParamSchema{
			"city": {Type: "string"},
		},
		Required: []string{"city"},
	})

	require.NotNil(t, format)
	assert.Equal(t, ResponseFormatJSONSchema, format.Type)
	assert.Equal(t, "city_response", format.Name)
	require.NotNil(t, format.Strict)
	assert.True(t, *format.Strict)
}

func TestBuildRequestResponseFormatJSONObject(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{name: ProviderOpenAI, model: "gpt-4.1-mini"}
	req, err := p.buildRequest(&ChatRequest{
		Messages:       []Message{UserText("hello")},
		ResponseFormat: JSONObjectFormat(),
	})
	require.NoError(t, err)
	require.NotNil(t, req.ResponseFormat)
	assert.Equal(t, openai.ChatCompletionResponseFormatTypeJSONObject, req.ResponseFormat.Type)
	assert.Nil(t, req.ResponseFormat.JSONSchema)
}

func TestBuildRequestResponseFormatJSONSchema(t *testing.T) {
	t.Parallel()

	p := &openaiProvider{name: ProviderOpenAI, model: "gpt-4.1-mini"}
	req, err := p.buildRequest(&ChatRequest{
		Messages: []Message{UserText("hello")},
		ResponseFormat: JSONSchemaFormatStrict("city_response", ParamSchema{
			Type: "object",
			Properties: map[string]ParamSchema{
				"city": {Type: "string"},
			},
			Required: []string{"city"},
		}),
	})
	require.NoError(t, err)
	require.NotNil(t, req.ResponseFormat)
	assert.Equal(t, openai.ChatCompletionResponseFormatTypeJSONSchema, req.ResponseFormat.Type)
	require.NotNil(t, req.ResponseFormat.JSONSchema)
	assert.Equal(t, "city_response", req.ResponseFormat.JSONSchema.Name)
	assert.True(t, req.ResponseFormat.JSONSchema.Strict)
	assert.NotNil(t, req.ResponseFormat.JSONSchema.Schema)
}
