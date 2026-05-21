package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextPart(t *testing.T) {
	t.Parallel()

	part := TextPart("describe this image")

	assert.Equal(t, ContentTypeText, part.Type)
	assert.Equal(t, "describe this image", part.Text)
	assert.Empty(t, part.ImageURL)
	assert.Nil(t, part.ImageData)
	assert.Empty(t, part.MIMEType)
	assert.Empty(t, part.ImageDetail)
}

func TestImageURLPart(t *testing.T) {
	t.Parallel()

	part := ImageURLPart("https://example.com/cat.png")

	assert.Equal(t, ContentTypeImageURL, part.Type)
	assert.Equal(t, "https://example.com/cat.png", part.ImageURL)
	assert.Nil(t, part.ImageData)
	assert.Empty(t, part.MIMEType)
	assert.Equal(t, ImageDetailAuto, part.ImageDetail)
}

func TestImageURLPartWithDetail(t *testing.T) {
	t.Parallel()

	part := ImageURLPartWithDetail("https://example.com/cat.png", ImageDetailHigh)

	assert.Equal(t, ContentTypeImageURL, part.Type)
	assert.Equal(t, "https://example.com/cat.png", part.ImageURL)
	assert.Equal(t, ImageDetailHigh, part.ImageDetail)
}

func TestImageDataPart(t *testing.T) {
	t.Parallel()

	data := []byte{0x89, 0x50, 0x4e, 0x47}
	part := ImageDataPart(data, "image/png")

	assert.Equal(t, ContentTypeImageURL, part.Type)
	assert.Empty(t, part.ImageURL)
	assert.Equal(t, data, part.ImageData)
	assert.Equal(t, "image/png", part.MIMEType)
	assert.Equal(t, ImageDetailAuto, part.ImageDetail)
}

func TestFileDataPart(t *testing.T) {
	t.Parallel()

	data := []byte("%PDF-1.7")
	part := FileDataPart(data, "application/pdf", "brief.pdf")

	assert.Equal(t, ContentTypeFile, part.Type)
	assert.Equal(t, data, part.FileData)
	assert.Equal(t, "application/pdf", part.MIMEType)
	assert.Equal(t, "brief.pdf", part.Filename)
	assert.Empty(t, part.FileURL)
	assert.Empty(t, part.FileID)
}

func TestFileURLPart(t *testing.T) {
	t.Parallel()

	part := FileURLPart("gs://bucket/brief.pdf", "application/pdf")

	assert.Equal(t, ContentTypeFile, part.Type)
	assert.Equal(t, "gs://bucket/brief.pdf", part.FileURL)
	assert.Equal(t, "application/pdf", part.MIMEType)
	assert.Nil(t, part.FileData)
	assert.Empty(t, part.FileID)
}

func TestFileIDPart(t *testing.T) {
	t.Parallel()

	part := FileIDPart("file_123")

	assert.Equal(t, ContentTypeFile, part.Type)
	assert.Equal(t, "file_123", part.FileID)
	assert.Empty(t, part.FileURL)
	assert.Nil(t, part.FileData)
}

func TestWithCacheControl(t *testing.T) {
	t.Parallel()

	part := WithCacheControl(TextPart("expensive context"), CacheControlEphemeral())

	assert.Equal(t, ContentTypeText, part.Type)
	require.NotNil(t, part.CacheControl)
	assert.Equal(t, CacheControlTypeEphemeral, part.CacheControl.Type)
}

func TestContentPartPreferredImageSource(t *testing.T) {
	t.Parallel()

	part := ContentPart{
		Type:      ContentTypeImageURL,
		ImageURL:  "https://example.com/cat.png",
		ImageData: []byte("raw-image"),
		MIMEType:  "image/png",
	}

	source, ok := part.preferredImageSource()
	require.True(t, ok)
	assert.Equal(t, "https://example.com/cat.png", source)
}

func TestContentPartPreferredImageSourceFallsBackToInlineData(t *testing.T) {
	t.Parallel()

	part := ContentPart{
		Type:      ContentTypeImageURL,
		ImageData: []byte("raw-image"),
		MIMEType:  "image/png",
	}

	source, ok := part.preferredImageSource()
	require.True(t, ok)
	assert.Equal(t, "data:image/png;base64,cmF3LWltYWdl", source)
}

func TestContentPartPreferredImageSourceZeroValue(t *testing.T) {
	t.Parallel()

	source, ok := (ContentPart{}).preferredImageSource()
	assert.False(t, ok)
	assert.Empty(t, source)
}

func TestUserText(t *testing.T) {
	t.Parallel()

	msg := UserText("hello")

	assert.Equal(t, RoleUser, msg.Role)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, TextPart("hello"), msg.Content[0])
}

func TestSystemText(t *testing.T) {
	t.Parallel()

	msg := SystemText("you are helpful")

	assert.Equal(t, RoleSystem, msg.Role)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, TextPart("you are helpful"), msg.Content[0])
}

func TestAssistantText(t *testing.T) {
	t.Parallel()

	msg := AssistantText("done")

	assert.Equal(t, RoleAssistant, msg.Role)
	require.Len(t, msg.Content, 1)
	assert.Equal(t, TextPart("done"), msg.Content[0])
}

func TestUserMessage(t *testing.T) {
	t.Parallel()

	msg := UserMessage(
		TextPart("describe"),
		ImageURLPart("https://example.com/cat.png"),
	)

	assert.Equal(t, RoleUser, msg.Role)
	require.Len(t, msg.Content, 2)
	assert.Equal(t, "describe", msg.Content[0].Text)
	assert.Equal(t, "https://example.com/cat.png", msg.Content[1].ImageURL)
}

func TestSystemMessageZeroValueUsesNonNilContent(t *testing.T) {
	t.Parallel()

	msg := SystemMessage()

	assert.Equal(t, RoleSystem, msg.Role)
	require.NotNil(t, msg.Content)
	assert.Empty(t, msg.Content)
}

func TestAssistantMessageZeroValueUsesNonNilContent(t *testing.T) {
	t.Parallel()

	msg := AssistantMessage()

	assert.Equal(t, RoleAssistant, msg.Role)
	require.NotNil(t, msg.Content)
	assert.Empty(t, msg.Content)
}
