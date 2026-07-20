package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

// 常用文件用途，对应 OpenAI 兼容 Files API 的 purpose 字段。
// 各平台接受的取值不同，Purpose 字段保持普通 string，不做枚举限制。
const (
	// FilePurposeFileExtract 是国内平台（Moonshot / 千问 / 智谱）的文档抽取用途。
	FilePurposeFileExtract = "file-extract"
	// FilePurposeUserData 是 OpenAI 官方推荐的通用用户数据用途。
	FilePurposeUserData = "user_data"
	// FilePurposeAssistants 是 OpenAI Assistants 场景的文件用途。
	FilePurposeAssistants = "assistants"
	// FilePurposeBatch 是 Batch API 输入文件的用途。
	FilePurposeBatch = "batch"
)

// FileUploadRequest 描述一次文件上传。
type FileUploadRequest struct {
	// Filename 是带扩展名的文件名，平台以此识别文档类型。
	Filename string
	// Data 是文件的完整内容。
	Data []byte
	// Purpose 声明文件用途，必填。国内文档问答平台使用 FilePurposeFileExtract。
	Purpose string
}

func (r *FileUploadRequest) validate() error {
	var errs []error

	if r.Filename == "" {
		errs = append(errs, errors.New("filename is required"))
	}
	if len(r.Data) == 0 {
		errs = append(errs, errors.New("file data is required"))
	}
	if r.Purpose == "" {
		errs = append(errs, errors.New("file purpose is required"))
	}

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("%w: %w", ErrInvalidRequest, errors.Join(errs...))
}

// FileObject 是平台返回的已上传文件元数据。
type FileObject struct {
	// ID 是平台分配的文件标识，用于内容抽取、fileid:// 引用与删除。
	ID string
	// Filename 是上传时的文件名。
	Filename string
	// Bytes 是文件大小（字节）。
	Bytes int64
	// Purpose 是上传时声明的文件用途。
	Purpose string
	// CreatedAt 是平台记录的创建时间（Unix 秒）。
	CreatedAt int64
}

// FileService 是 OpenAI 兼容 Files API 的文件管理接口。
//
// NewProvider / NewProviderFromPreset / NewAzureOpenAIProvider /
// NewBedrockOpenAIProvider 返回的 Provider 均实现本接口，通过类型断言获取：
//
//	fs, ok := p.(provider.FileService)
//
// 注意：WithRetry / WithMiddlewares / NewFallbackProvider 等包装后的 Provider
// 不再实现本接口，请在包装前保留原始句柄用于文件操作。平台是否真正提供
// Files API 以 CapabilityFileUpload 标注和平台文档为准（如 DeepSeek 无此接口，
// 调用会返回平台侧错误）。
//
// 上传后的文件进入对话有两种平台约定（消息内 file part 在 OpenAI 兼容
// Chat Completions 路径不受支持）：
//   - 千问 qwen-long：用 FileIDSystemMessage 把文件 ID 写入 system 消息；
//   - Moonshot / 智谱：用 FileContent 拉取平台抽取的文档内容，作为
//     system 消息文本传入。
type FileService interface {
	// UploadFile 上传文件并返回平台分配的文件元数据。
	UploadFile(ctx context.Context, req *FileUploadRequest) (*FileObject, error)

	// FileContent 返回文件内容。对 purpose 为 file-extract 的文件，
	// Moonshot / 智谱返回平台抽取后的文档文本，可直接作为 system 消息内容。
	FileContent(ctx context.Context, fileID string) (string, error)

	// DeleteFile 删除已上传的文件。平台通常限制文件保有量，
	// 抽取完成后建议及时清理。
	DeleteFile(ctx context.Context, fileID string) error
}

var _ FileService = (*openaiProvider)(nil)

// FileIDSystemMessage 按阿里百炼 qwen-long 的约定构造文件引用消息：
// system 消息内容为 "fileid://<ID>"，多个文件以英文逗号分隔。
// 文件需先通过 UploadFile 以 FilePurposeFileExtract 用途上传。
func FileIDSystemMessage(fileID string, more ...string) Message {
	refs := make([]string, 0, 1+len(more))
	refs = append(refs, "fileid://"+fileID)
	for _, id := range more {
		refs = append(refs, "fileid://"+id)
	}
	return SystemText(strings.Join(refs, ","))
}

// UploadFile 实现 FileService。
func (p *openaiProvider) UploadFile(ctx context.Context, req *FileUploadRequest) (*FileObject, error) {
	if p == nil {
		return nil, ErrNilProvider
	}
	if req == nil {
		return nil, ErrNilFileUploadRequest
	}
	if err := req.validate(); err != nil {
		return nil, err
	}

	file, err := p.client.CreateFileBytes(ctx, openai.FileBytesRequest{
		Name:    req.Filename,
		Bytes:   req.Data,
		Purpose: openai.PurposeType(req.Purpose),
	})
	if err != nil {
		return nil, WrapProviderError(p.name, err)
	}

	return &FileObject{
		ID:        file.ID,
		Filename:  file.FileName,
		Bytes:     int64(file.Bytes),
		Purpose:   file.Purpose,
		CreatedAt: file.CreatedAt,
	}, nil
}

// FileContent 实现 FileService。
func (p *openaiProvider) FileContent(ctx context.Context, fileID string) (string, error) {
	if p == nil {
		return "", ErrNilProvider
	}
	if err := validateFileID(fileID); err != nil {
		return "", err
	}

	raw, err := p.client.GetFileContent(ctx, fileID)
	if err != nil {
		return "", WrapProviderError(p.name, err)
	}
	defer raw.Close()

	data, err := io.ReadAll(raw)
	if err != nil {
		return "", WrapProviderError(p.name, err)
	}

	return string(data), nil
}

// DeleteFile 实现 FileService。
func (p *openaiProvider) DeleteFile(ctx context.Context, fileID string) error {
	if p == nil {
		return ErrNilProvider
	}
	if err := validateFileID(fileID); err != nil {
		return err
	}

	if err := p.client.DeleteFile(ctx, fileID); err != nil {
		return WrapProviderError(p.name, err)
	}

	return nil
}

func validateFileID(fileID string) error {
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("%w: file id is required", ErrInvalidRequest)
	}
	return nil
}
