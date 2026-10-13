package einoadapter

import (
	"encoding/base64"
	"reflect"

	"github.com/cloudwego/eino/schema"
	"github.com/gtkit/go-llm-provider/v2/provider"
)

func inputMessage(message provider.Message) (*schema.Message, error) {
	out := &schema.Message{Role: schema.RoleType(message.Role), ReasoningContent: message.Reasoning, ToolCallID: message.ToolCallID, ToolName: message.ToolName}
	if err := setInputContent(out, message.Content); err != nil {
		return nil, err
	}
	for i, call := range message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, nativeCall(i, call))
	}
	e := event{Kind: "input", Blocks: message.ThinkingBlocks, Tools: callSignatures(message.ToolCalls)}
	// A single ordinary text part is completely represented by Content.
	if len(message.Content) != 1 || !reflect.DeepEqual(message.Content[0], provider.TextPart(message.Content[0].Text)) {
		e.Content = &message.Content
		e.HasContent = true
	}
	if e.Content != nil || len(e.Blocks) > 0 || len(e.Tools) > 0 {
		var err error
		out.Extra, err = eventExtra(e)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func setInputContent(out *schema.Message, parts []provider.ContentPart) error {
	for _, part := range parts {
		switch part.Type {
		case provider.ContentTypeText:
			out.Content += part.Text
		case provider.ContentTypeImageURL, provider.ContentTypeFile:
		default:
			return invalid("Content.Type unsupported")
		}
	}
	if out.Role != schema.User || (len(parts) == 1 && parts[0].Type == provider.ContentTypeText) {
		return nil
	}
	out.Content = ""
	for _, part := range parts {
		p := schema.MessageInputPart{Type: schema.ChatMessagePartType(part.Type), Text: part.Text}
		switch part.Type {
		case provider.ContentTypeImageURL:
			p.Image = &schema.MessageInputImage{MessagePartCommon: media(part.ImageURL, part.ImageData, part.MIMEType), Detail: schema.ImageURLDetail(part.ImageDetail)}
		case provider.ContentTypeFile:
			p.Type = schema.ChatMessagePartTypeFileURL
			p.File = &schema.MessageInputFile{MessagePartCommon: media(part.FileURL, part.FileData, part.MIMEType), Name: part.Filename}
		}
		out.UserInputMultiContent = append(out.UserInputMultiContent, p)
	}
	return nil
}

func media(url string, data []byte, mime string) schema.MessagePartCommon {
	out := schema.MessagePartCommon{MIMEType: mime}
	if url != "" {
		out.URL = new(url)
	}
	if data != nil {
		out.Base64Data = new(base64.StdEncoding.EncodeToString(data))
	}
	return out
}

func messageToProvider(msg *schema.Message) (provider.Message, error) {
	if msg == nil {
		return provider.Message{}, invalid("nil Message")
	}
	switch msg.Role {
	case schema.System, schema.User, schema.Assistant, schema.Tool:
	default:
		return provider.Message{}, invalid("Message.Role")
	}
	if msg.Name != "" || len(msg.MultiContent) > 0 || len(msg.AssistantGenMultiContent) > 0 {
		return provider.Message{}, invalid("Message.Name/MultiContent/AssistantGenMultiContent unsupported")
	}
	events, err := readEvents(msg.Extra)
	if err != nil {
		return provider.Message{}, err
	}
	out := provider.Message{Role: provider.Role(msg.Role), Reasoning: msg.ReasoningContent, ToolCallID: msg.ToolCallID, ToolName: msg.ToolName}
	if len(events) > 0 && events[0].Kind != "input" {
		response, err := ToResponse(msg)
		if err != nil {
			return provider.Message{}, err
		}
		return response.AssistantMessage(), nil
	}
	out.Content, err = contentFromEino(msg)
	if err != nil {
		return provider.Message{}, err
	}
	out.ToolCalls, err = callsFromEino(msg.ToolCalls)
	if err != nil {
		return provider.Message{}, err
	}
	if err := restoreInput(&out, msg, events); err != nil {
		return provider.Message{}, err
	}

	return out, nil
}

func restoreInput(out *provider.Message, msg *schema.Message, events []event) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) != 1 {
		return invalid("multiple input extensions")
	}
	e := events[0]
	if e.HasContent {
		if e.Content == nil {
			e.Content = new([]provider.ContentPart)
		}
		preview := &schema.Message{Role: msg.Role}
		if err := setInputContent(preview, *e.Content); err != nil {
			return err
		}
		if preview.Content != msg.Content || !reflect.DeepEqual(preview.UserInputMultiContent, msg.UserInputMultiContent) {
			return invalid("modified native content conflicts with extension")
		}
		out.Content = *e.Content
	}
	out.ThinkingBlocks = e.Blocks
	return applySignatures(out.ToolCalls, e.Tools)
}

func contentFromEino(msg *schema.Message) ([]provider.ContentPart, error) {
	if len(msg.UserInputMultiContent) == 0 {
		return []provider.ContentPart{provider.TextPart(msg.Content)}, nil
	}
	if msg.Content != "" || msg.Role != schema.User {
		return nil, invalid("mixed content or non-user multimodal input")
	}
	out := make([]provider.ContentPart, len(msg.UserInputMultiContent))
	for i, p := range msg.UserInputMultiContent {
		part, err := partFromEino(p)
		if err != nil {
			return nil, err
		}
		out[i] = part
	}
	return out, nil
}

func partFromEino(p schema.MessageInputPart) (provider.ContentPart, error) {
	if len(p.Extra) > 0 || p.Audio != nil || p.Video != nil || p.ToolSearchResult != nil {
		return provider.ContentPart{}, invalid("multimodal input unsupported fields")
	}
	var out provider.ContentPart
	var media schema.MessagePartCommon
	switch p.Type {
	case schema.ChatMessagePartTypeText:
		if p.Image != nil || p.File != nil {
			return out, invalid("text part with media")
		}
		return provider.TextPart(p.Text), nil
	case schema.ChatMessagePartTypeImageURL:
		if p.Image == nil || p.File != nil || p.Text != "" {
			return out, invalid("Image nil/conflicting fields")
		}
		media = p.Image.MessagePartCommon
		out = provider.ContentPart{Type: provider.ContentTypeImageURL, ImageDetail: provider.ImageDetail(p.Image.Detail)}
	case schema.ChatMessagePartTypeFileURL:
		if p.File == nil || p.Image != nil || p.Text != "" {
			return out, invalid("File nil/conflicting fields")
		}
		media = p.File.MessagePartCommon
		out = provider.ContentPart{Type: provider.ContentTypeFile, Filename: p.File.Name}
	default:
		return out, invalid("multimodal type unsupported")
	}
	if err := setMedia(&out, media); err != nil {
		return out, err
	}
	return out, nil
}

func setMedia(out *provider.ContentPart, media schema.MessagePartCommon) error {
	//nolint:staticcheck // Reject deprecated metadata rather than silently losing it.
	if len(media.Extra) > 0 {
		return invalid("media Extra unsupported")
	}
	out.MIMEType = media.MIMEType
	if media.URL != nil {
		if out.Type == provider.ContentTypeFile {
			out.FileURL = *media.URL
		} else {
			out.ImageURL = *media.URL
		}
	}
	if media.Base64Data == nil {
		return nil
	}
	data, err := base64.StdEncoding.DecodeString(*media.Base64Data)
	if err != nil {
		return invalid("invalid media Base64Data")
	}
	if out.Type == provider.ContentTypeFile {
		out.FileData = data
	} else {
		out.ImageData = data
	}
	return nil
}
