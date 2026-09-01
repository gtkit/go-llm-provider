package provider

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const defaultAzureAPIVersion = "2024-10-21"

// AzureOpenAIConfig 配置 Azure OpenAI Provider。
type AzureOpenAIConfig struct {
	APIKey      string
	Endpoint    string
	Deployment  string
	APIVersion  string
	HTTPClient  HTTPDoer
	ModelMapper func(model string) string
}

// Validate 检查 Azure OpenAI 必需配置字段是否缺失。
func (cfg AzureOpenAIConfig) Validate() error {
	var errs []error
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("api key is required"))
	}
	if cfg.Endpoint == "" {
		errs = append(errs, errors.New("endpoint is required"))
	}
	if cfg.Deployment == "" && cfg.ModelMapper == nil {
		errs = append(errs, errors.New("deployment is required"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalidProviderConfig, errors.Join(errs...))
}

// NewAzureOpenAIProvider 创建 Azure OpenAI Provider。
func NewAzureOpenAIProvider(cfg AzureOpenAIConfig) (Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	ocfg := openai.DefaultAzureConfig(cfg.APIKey, endpoint)
	if cfg.APIVersion == "" {
		cfg.APIVersion = defaultAzureAPIVersion
	}
	ocfg.APIVersion = cfg.APIVersion
	if cfg.HTTPClient == nil {
		ocfg.HTTPClient = DefaultHTTPClient()
	} else {
		ocfg.HTTPClient = cfg.HTTPClient
	}
	if cfg.ModelMapper != nil {
		ocfg.AzureModelMapperFunc = cfg.ModelMapper
	} else {
		deployment := cfg.Deployment
		ocfg.AzureModelMapperFunc = func(string) string {
			return deployment
		}
	}
	model := cfg.Deployment
	if model == "" {
		model = string(ProviderAzureOpenAI)
	}
	return &openaiProvider{
		name:   ProviderAzureOpenAI,
		model:  model,
		client: openai.NewClientWithConfig(ocfg),
	}, nil
}

// BedrockOpenAIConfig 配置 Amazon Bedrock OpenAI 兼容 Provider。
type BedrockOpenAIConfig struct {
	APIKey     string
	Region     string
	Model      string
	BaseURL    string
	HTTPClient HTTPDoer

	// SupportsReasoningEffort 声明该 Bedrock 部署接受 OpenAI 标准的
	// reasoning_effort 字段，从而允许使用 Thinking.Effort。
	// 语义同 ProviderConfig.SupportsReasoningEffort：Bedrock 上能否使用该字段
	// 取决于所选的底层模型，库不代为断言，交由知情的调用方声明。
	SupportsReasoningEffort bool
}

// Validate 检查 Bedrock OpenAI 兼容 API 的必需配置字段是否缺失。
func (cfg BedrockOpenAIConfig) Validate() error {
	var errs []error
	if cfg.APIKey == "" {
		errs = append(errs, errors.New("api key is required"))
	}
	if cfg.Region == "" && cfg.BaseURL == "" {
		errs = append(errs, errors.New("region is required"))
	}
	if cfg.Model == "" {
		errs = append(errs, errors.New("model is required"))
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrInvalidProviderConfig, errors.Join(errs...))
}

// NewBedrockOpenAIProvider 创建 Amazon Bedrock OpenAI 兼容 API Provider。
func NewBedrockOpenAIProvider(cfg BedrockOpenAIConfig) (Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://bedrock-mantle." + cfg.Region + ".api.aws/v1"
	}
	if _, err := url.ParseRequestURI(baseURL); err != nil {
		return nil, fmt.Errorf("%w: invalid bedrock base URL: %w", ErrInvalidProviderConfig, err)
	}
	if host := hostFromURL(baseURL); host == "" {
		return nil, fmt.Errorf("%w: invalid bedrock base URL host", ErrInvalidProviderConfig)
	}
	return NewProvider(ProviderConfig{
		Name:                    ProviderBedrock,
		BaseURL:                 baseURL,
		APIKey:                  cfg.APIKey,
		Model:                   cfg.Model,
		HTTPClient:              cfg.HTTPClient,
		SupportsReasoningEffort: cfg.SupportsReasoningEffort,
	})
}

func hostFromURL(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if u.Host == "" {
		return ""
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err == nil {
		return host
	}
	return u.Host
}
