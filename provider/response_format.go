package provider

import (
	"fmt"

	"github.com/gtkit/json"
)

// ResponseFormatType selects the chat completion output format.
type ResponseFormatType string

const (
	// ResponseFormatText requests plain text output.
	ResponseFormatText ResponseFormatType = "text"
	// ResponseFormatJSONObject requests valid JSON object output.
	ResponseFormatJSONObject ResponseFormatType = "json_object"
	// ResponseFormatJSONSchema requests structured JSON output validated by schema.
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// ResponseFormat describes the requested provider output format.
type ResponseFormat struct {
	Type   ResponseFormatType
	Schema any
	Name   string
	Strict *bool
}

// TextFormat creates a plain text response format.
func TextFormat() *ResponseFormat {
	return &ResponseFormat{Type: ResponseFormatText}
}

// JSONObjectFormat creates a JSON object response format.
func JSONObjectFormat() *ResponseFormat {
	return &ResponseFormat{Type: ResponseFormatJSONObject}
}

// JSONSchemaFormat creates a JSON schema response format.
func JSONSchemaFormat(name string, schema any) *ResponseFormat {
	return &ResponseFormat{
		Type:   ResponseFormatJSONSchema,
		Name:   name,
		Schema: schema,
	}
}

// JSONSchemaFormatStrict creates a strict JSON schema response format.
func JSONSchemaFormatStrict(name string, schema any) *ResponseFormat {
	strict := true
	return &ResponseFormat{
		Type:   ResponseFormatJSONSchema,
		Name:   name,
		Schema: schema,
		Strict: &strict,
	}
}

type responseFormatSchema []byte

func (s responseFormatSchema) MarshalJSON() ([]byte, error) {
	if len(s) == 0 {
		return []byte("null"), nil
	}
	return s, nil
}

func marshalResponseFormatSchema(schema any) (responseFormatSchema, error) {
	if schema == nil {
		return nil, nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal response format schema: %w", err)
	}
	return responseFormatSchema(data), nil
}
