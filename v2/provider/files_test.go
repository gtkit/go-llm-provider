package provider

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFileServiceForTest 启动一个模拟平台端点并返回其上的 FileService。
func newFileServiceForTest(t *testing.T, handler http.HandlerFunc) FileService {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	p, err := NewProvider(ProviderConfig{
		Name:    ProviderMoonshot,
		BaseURL: srv.URL,
		APIKey:  "sk-test",
		Model:   "kimi-k2-turbo-preview",
	})
	require.NoError(t, err)

	fs, ok := p.(FileService)
	require.True(t, ok, "openai compatible provider must implement FileService")
	return fs
}

func TestUploadFile(t *testing.T) {
	t.Parallel()

	var (
		gotMethod   string
		gotPath     string
		gotAuth     string
		gotPurpose  string
		gotFilename string
		gotData     []byte
	)
	fs := newFileServiceForTest(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")

		assert.NoError(t, r.ParseMultipartForm(1<<20))
		gotPurpose = r.FormValue("purpose")

		file, header, err := r.FormFile("file")
		assert.NoError(t, err)
		if err == nil {
			gotFilename = header.Filename
			gotData, err = io.ReadAll(file)
			assert.NoError(t, err)
			assert.NoError(t, file.Close())
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-abc","object":"file","bytes":13,"created_at":1721451600,"filename":"brief.pdf","purpose":"file-extract"}`))
	})

	obj, err := fs.UploadFile(t.Context(), &FileUploadRequest{
		Filename: "brief.pdf",
		Data:     []byte("%PDF-1.7 fake"),
		Purpose:  FilePurposeFileExtract,
	})
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/files", gotPath)
	assert.Equal(t, "Bearer sk-test", gotAuth)
	assert.Equal(t, FilePurposeFileExtract, gotPurpose)
	assert.Equal(t, "brief.pdf", gotFilename)
	assert.Equal(t, "%PDF-1.7 fake", string(gotData))

	assert.Equal(t, &FileObject{
		ID:        "file-abc",
		Filename:  "brief.pdf",
		Bytes:     13,
		Purpose:   "file-extract",
		CreatedAt: 1721451600,
	}, obj)
}

func TestUploadFileValidation(t *testing.T) {
	t.Parallel()

	fs := newFileServiceForTest(t, func(http.ResponseWriter, *http.Request) {
		t.Error("request must not reach the platform")
	})

	t.Run("nil request", func(t *testing.T) {
		t.Parallel()
		_, err := fs.UploadFile(t.Context(), nil)
		assert.ErrorIs(t, err, ErrNilFileUploadRequest)
	})

	t.Run("missing fields", func(t *testing.T) {
		t.Parallel()
		_, err := fs.UploadFile(t.Context(), &FileUploadRequest{})
		require.ErrorIs(t, err, ErrInvalidRequest)
		require.ErrorContains(t, err, "filename is required")
		require.ErrorContains(t, err, "file data is required")
		require.ErrorContains(t, err, "file purpose is required")
	})

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		var p *openaiProvider
		_, err := p.UploadFile(t.Context(), &FileUploadRequest{
			Filename: "a.txt",
			Data:     []byte("x"),
			Purpose:  FilePurposeFileExtract,
		})
		assert.ErrorIs(t, err, ErrNilProvider)
	})
}

func TestUploadFileAPIError(t *testing.T) {
	t.Parallel()

	fs := newFileServiceForTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key","type":"invalid_request_error","code":"invalid_api_key"}}`))
	})

	_, err := fs.UploadFile(t.Context(), &FileUploadRequest{
		Filename: "brief.pdf",
		Data:     []byte("%PDF-1.7"),
		Purpose:  FilePurposeFileExtract,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrAuth)

	var pe *ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, ProviderMoonshot, pe.Provider)
	assert.Equal(t, http.StatusUnauthorized, pe.StatusCode)
}

func TestFileContent(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
	)
	fs := newFileServiceForTest(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("抽取后的文档内容"))
	})

	content, err := fs.FileContent(t.Context(), "file-abc")
	require.NoError(t, err)

	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/files/file-abc/content", gotPath)
	assert.Equal(t, "抽取后的文档内容", content)
}

func TestFileContentValidation(t *testing.T) {
	t.Parallel()

	fs := newFileServiceForTest(t, func(http.ResponseWriter, *http.Request) {
		t.Error("request must not reach the platform")
	})

	t.Run("empty file id", func(t *testing.T) {
		t.Parallel()
		_, err := fs.FileContent(t.Context(), "  ")
		assert.ErrorIs(t, err, ErrInvalidRequest)
	})

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		var p *openaiProvider
		_, err := p.FileContent(t.Context(), "file-abc")
		assert.ErrorIs(t, err, ErrNilProvider)
	})
}

func TestFileContentAPIError(t *testing.T) {
	t.Parallel()

	fs := newFileServiceForTest(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"message":"file not found","type":"invalid_request_error"}}`))
	})

	_, err := fs.FileContent(t.Context(), "file-missing")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidRequest)

	var pe *ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, http.StatusNotFound, pe.StatusCode)
}

func TestDeleteFile(t *testing.T) {
	t.Parallel()

	var (
		gotMethod string
		gotPath   string
	)
	fs := newFileServiceForTest(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-abc","object":"file","deleted":true}`))
	})

	require.NoError(t, fs.DeleteFile(t.Context(), "file-abc"))
	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/files/file-abc", gotPath)
}

func TestDeleteFileValidationAndError(t *testing.T) {
	t.Parallel()

	t.Run("empty file id", func(t *testing.T) {
		t.Parallel()
		fs := newFileServiceForTest(t, func(http.ResponseWriter, *http.Request) {
			t.Error("request must not reach the platform")
		})
		assert.ErrorIs(t, fs.DeleteFile(t.Context(), ""), ErrInvalidRequest)
	})

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		var p *openaiProvider
		assert.ErrorIs(t, p.DeleteFile(t.Context(), "file-abc"), ErrNilProvider)
	})

	t.Run("api error", func(t *testing.T) {
		t.Parallel()
		fs := newFileServiceForTest(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"boom","type":"server_error"}}`))
		})

		err := fs.DeleteFile(t.Context(), "file-abc")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrServerError)
	})
}

func TestFileIDSystemMessage(t *testing.T) {
	t.Parallel()

	t.Run("single file", func(t *testing.T) {
		t.Parallel()
		msg := FileIDSystemMessage("file-fe-abc")
		assert.Equal(t, RoleSystem, msg.Role)
		require.Len(t, msg.Content, 1)
		assert.Equal(t, "fileid://file-fe-abc", msg.Content[0].Text)
	})

	t.Run("multiple files joined by comma", func(t *testing.T) {
		t.Parallel()
		msg := FileIDSystemMessage("file-fe-a", "file-fe-b", "file-fe-c")
		require.Len(t, msg.Content, 1)
		assert.Equal(t, "fileid://file-fe-a,fileid://file-fe-b,fileid://file-fe-c", msg.Content[0].Text)
	})
}

func TestFileServiceInterfaceBoundaries(t *testing.T) {
	t.Parallel()

	base, err := NewProvider(ProviderConfig{
		Name:   ProviderQwen,
		APIKey: "sk-test",
		Model:  "qwen-long",
	})
	require.NoError(t, err)

	t.Run("raw provider implements FileService", func(t *testing.T) {
		t.Parallel()
		_, ok := base.(FileService)
		assert.True(t, ok)
	})

	t.Run("wrapped provider loses FileService", func(t *testing.T) {
		t.Parallel()
		wrapped := WithRetry(base, RetryOptions{})
		_, ok := wrapped.(FileService)
		assert.False(t, ok, "retry wrapper does not forward FileService; keep the raw handle for file operations")
	})
}

func TestFileUploadCapabilityPresets(t *testing.T) {
	t.Parallel()

	supported := []ProviderName{ProviderOpenAI, ProviderMoonshot, ProviderQwen, ProviderZhipu}
	for _, name := range supported {
		caps, ok := ModelCapabilitiesFromPreset(name)
		require.True(t, ok, name)
		assert.True(t, caps.Supports(CapabilityFileUpload), "%s preset should declare file upload", name)
	}

	unsupported := []ProviderName{ProviderDeepSeek, ProviderQianfan, ProviderSiliconFlow, ProviderAnthropic, ProviderGemini, ProviderOllama}
	for _, name := range unsupported {
		caps, ok := ModelCapabilitiesFromPreset(name)
		require.True(t, ok, name)
		assert.False(t, caps.Supports(CapabilityFileUpload), "%s preset should not declare file upload", name)
	}
}

func TestBuildOpenAIMessageFilePartStillRejected(t *testing.T) {
	t.Parallel()

	_, err := buildOpenAIMessage(UserMessage(
		TextPart("总结这份文件"),
		FileIDPart("file-abc"),
	))
	require.Error(t, err)
	require.ErrorIs(t, err, ErrInvalidRequest)

	_, err = buildOpenAIMessage(UserMessage(
		FileDataPart([]byte("%PDF-1.7"), "application/pdf", "brief.pdf"),
	))
	require.ErrorIs(t, err, ErrInvalidRequest)
}

func TestUploadFileNetworkErrorMapsToProviderError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // 立即关闭,制造连接失败

	p, err := NewProvider(ProviderConfig{
		Name:    ProviderZhipu,
		BaseURL: srv.URL,
		APIKey:  "sk-test",
		Model:   "glm-5.1",
	})
	require.NoError(t, err)

	fs, ok := p.(FileService)
	require.True(t, ok)

	_, err = fs.UploadFile(t.Context(), &FileUploadRequest{
		Filename: "a.txt",
		Data:     []byte("x"),
		Purpose:  FilePurposeFileExtract,
	})
	require.Error(t, err)

	var pe *ProviderError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, ProviderZhipu, pe.Provider)
}
