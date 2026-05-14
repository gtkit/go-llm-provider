package provider

import (
	"net/http"
	"strings"
)

// ResponseMetadata contains provider response diagnostics that are safe to expose.
type ResponseMetadata struct {
	Provider   ProviderName
	Model      string
	RequestID  string
	Headers    http.Header
	StatusCode int
	Status     string
}

// Header returns the first whitelisted response header value for key.
func (m ResponseMetadata) Header(key string) string {
	if len(m.Headers) == 0 {
		return ""
	}
	return m.Headers.Get(key)
}

var metadataHeaderAllowlist = map[string]struct{}{
	"x-request-id":                   {},
	"x-requestid":                    {},
	"x-correlation-id":               {},
	"x-amzn-requestid":               {},
	"request-id":                     {},
	"openai-request-id":              {},
	"x-ratelimit-limit-requests":     {},
	"x-ratelimit-remaining-requests": {},
	"x-ratelimit-reset-requests":     {},
	"x-ratelimit-limit-tokens":       {},
	"x-ratelimit-remaining-tokens":   {},
	"x-ratelimit-reset-tokens":       {},
	"x-ratelimit-limit-reqs":         {},
}

func metadataFromHeader(provider ProviderName, model string, header http.Header) ResponseMetadata {
	return ResponseMetadata{
		Provider:  provider,
		Model:     model,
		RequestID: requestIDFromHeader(header),
		Headers:   cloneMetadataHeaders(header),
	}
}

func requestIDFromHeader(header http.Header) string {
	for _, key := range []string{
		"x-request-id",
		"x-requestid",
		"x-correlation-id",
		"x-amzn-requestid",
		"request-id",
		"openai-request-id",
	} {
		if value := header.Get(key); value != "" {
			return value
		}
	}
	return ""
}

func cloneMetadataHeaders(header http.Header) http.Header {
	if len(header) == 0 {
		return nil
	}

	out := make(http.Header)
	for key, values := range header {
		normalized := strings.ToLower(key)
		if _, ok := metadataHeaderAllowlist[normalized]; !ok {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}
