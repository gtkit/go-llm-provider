package provider

import (
	"encoding/base64"
	"fmt"
)

// ContentType identifies the semantic type of a message content part.
type ContentType string

const (
	// ContentTypeText represents plain text content.
	ContentTypeText ContentType = "text"
	// ContentTypeImageURL represents image content referenced by URL or inline data URL.
	ContentTypeImageURL ContentType = "image_url"
	// ContentTypeFile 表示通过 ID、URL 或内联字节引用的文件内容。
	ContentTypeFile ContentType = "file"
)

// ImageDetail controls provider-side image fidelity hints.
type ImageDetail string

const (
	// ImageDetailAuto lets the provider choose an appropriate image detail level.
	ImageDetailAuto ImageDetail = "auto"
	// ImageDetailLow requests lower-cost image understanding.
	ImageDetailLow ImageDetail = "low"
	// ImageDetailHigh requests higher-fidelity image understanding.
	ImageDetailHigh ImageDetail = "high"
)

// ContentPart is one multimodal content fragment inside a message.
type ContentPart struct {
	Type         ContentType
	Text         string
	ImageURL     string
	ImageData    []byte
	FileURL      string
	FileData     []byte
	FileID       string
	Filename     string
	MIMEType     string
	ImageDetail  ImageDetail
	CacheControl *CacheControl
}

// CacheControlType 标识供应商侧的提示词缓存行为。
type CacheControlType string

const (
	// CacheControlTypeEphemeral 请求供应商侧启用临时提示词缓存。
	CacheControlTypeEphemeral CacheControlType = "ephemeral"
)

// CacheControl 描述内容片段的供应商侧提示词缓存提示。
type CacheControl struct {
	Type CacheControlType
}

// TextPart creates a text content part.
func TextPart(text string) ContentPart {
	return ContentPart{
		Type: ContentTypeText,
		Text: text,
	}
}

// ImageURLPart creates an image part using the default auto detail level.
func ImageURLPart(url string) ContentPart {
	return ImageURLPartWithDetail(url, ImageDetailAuto)
}

// ImageURLPartWithDetail creates an image part with an explicit detail level.
func ImageURLPartWithDetail(url string, detail ImageDetail) ContentPart {
	if detail == "" {
		detail = ImageDetailAuto
	}

	return ContentPart{
		Type:        ContentTypeImageURL,
		ImageURL:    url,
		ImageDetail: detail,
	}
}

// ImageDataPart creates an inline image part that will be sent as a data URL.
func ImageDataPart(data []byte, mimeType string) ContentPart {
	return ContentPart{
		Type:        ContentTypeImageURL,
		ImageData:   data,
		MIMEType:    mimeType,
		ImageDetail: ImageDetailAuto,
	}
}

// FileDataPart 创建一个内联文件内容片段。
func FileDataPart(data []byte, mimeType, filename string) ContentPart {
	return ContentPart{
		Type:     ContentTypeFile,
		FileData: data,
		MIMEType: mimeType,
		Filename: filename,
	}
}

// FileURLPart 创建一个通过 URL 引用的文件内容片段。
func FileURLPart(url, mimeType string) ContentPart {
	return ContentPart{
		Type:     ContentTypeFile,
		FileURL:  url,
		MIMEType: mimeType,
	}
}

// FileIDPart 创建一个通过供应商文件 ID 引用的文件内容片段。
func FileIDPart(id string) ContentPart {
	return ContentPart{
		Type:   ContentTypeFile,
		FileID: id,
	}
}

// CacheControlEphemeral 创建一个临时提示词缓存控制提示。
func CacheControlEphemeral() *CacheControl {
	return &CacheControl{Type: CacheControlTypeEphemeral}
}

// WithCacheControl 返回附加了供应商侧提示词缓存控制的内容片段。
func WithCacheControl(part ContentPart, control *CacheControl) ContentPart {
	part.CacheControl = control
	return part
}

// UserText creates a user message with a single text part.
func UserText(text string) Message {
	return UserMessage(TextPart(text))
}

// SystemText creates a system message with a single text part.
func SystemText(text string) Message {
	return SystemMessage(TextPart(text))
}

// AssistantText creates an assistant message with a single text part.
func AssistantText(text string) Message {
	return AssistantMessage(TextPart(text))
}

// UserMessage creates a user message from arbitrary content parts.
func UserMessage(parts ...ContentPart) Message {
	return Message{
		Role:    RoleUser,
		Content: nonNilParts(parts),
	}
}

// SystemMessage creates a system message from arbitrary content parts.
func SystemMessage(parts ...ContentPart) Message {
	return Message{
		Role:    RoleSystem,
		Content: nonNilParts(parts),
	}
}

// AssistantMessage creates an assistant message from arbitrary content parts.
func AssistantMessage(parts ...ContentPart) Message {
	return Message{
		Role:    RoleAssistant,
		Content: nonNilParts(parts),
	}
}

func nonNilParts(parts []ContentPart) []ContentPart {
	if len(parts) == 0 {
		return []ContentPart{}
	}
	return append([]ContentPart(nil), parts...)
}

func (p ContentPart) preferredImageSource() (string, bool) {
	if p.ImageURL != "" {
		return p.ImageURL, true
	}
	if len(p.ImageData) == 0 || p.MIMEType == "" {
		return "", false
	}

	return fmt.Sprintf(
		"data:%s;base64,%s",
		p.MIMEType,
		base64.StdEncoding.EncodeToString(p.ImageData),
	), true
}
