package provider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSchemaFromType(t *testing.T) {
	t.Parallel()

	type address struct {
		City string `json:"city"`
		Zip  string `json:"zip,omitempty"`
	}
	type person struct {
		Name     string   `json:"name"`
		Age      int      `json:"age"`
		Score    float64  `json:"score"`
		Active   bool     `json:"active"`
		Tags     []string `json:"tags"`
		Address  address  `json:"address"`
		Nickname *string  `json:"nickname"`
		Color    string   `json:"color" jsonschema:"enum=red|green|blue"`
		Secret   string   `json:"-"`
		internal string   //nolint:unused // 故意保留：验证非导出字段被 schema 跳过
	}

	schema, err := SchemaFromType[person]()
	require.NoError(t, err)

	assert.Equal(t, "object", schema.Type)
	require.NotNil(t, schema.AdditionalProperties)
	assert.False(t, *schema.AdditionalProperties)

	assert.Equal(t, "string", schema.Properties["name"].Type)
	assert.Equal(t, "integer", schema.Properties["age"].Type)
	assert.Equal(t, "number", schema.Properties["score"].Type)
	assert.Equal(t, "boolean", schema.Properties["active"].Type)
	assert.Equal(t, "string", schema.Properties["nickname"].Type)

	require.Equal(t, "array", schema.Properties["tags"].Type)
	require.NotNil(t, schema.Properties["tags"].Items)
	assert.Equal(t, "string", schema.Properties["tags"].Items.Type)

	addr := schema.Properties["address"]
	assert.Equal(t, "object", addr.Type)
	assert.Equal(t, "string", addr.Properties["city"].Type)
	assert.ElementsMatch(t, []string{"city"}, addr.Required)

	assert.Equal(t, []string{"red", "green", "blue"}, schema.Properties["color"].Enum)

	assert.NotContains(t, schema.Properties, "Secret")
	assert.NotContains(t, schema.Properties, "-")
	assert.NotContains(t, schema.Properties, "internal")

	// 指针与 omitempty 字段不进 required。
	assert.ElementsMatch(t,
		[]string{"name", "age", "score", "active", "tags", "address", "color"},
		schema.Required,
	)
}

func TestSchemaFromTypeTimeAndMap(t *testing.T) {
	t.Parallel()

	type event struct {
		At    time.Time      `json:"at"`
		Attrs map[string]int `json:"attrs"`
	}
	schema, err := SchemaFromType[event]()
	require.NoError(t, err)

	assert.Equal(t, "string", schema.Properties["at"].Type)

	attrs := schema.Properties["attrs"]
	assert.Equal(t, "object", attrs.Type)
	require.NotNil(t, attrs.AdditionalProperties)
	assert.True(t, *attrs.AdditionalProperties)
}

func TestSchemaFromTypeByteHandling(t *testing.T) {
	t.Parallel()

	type withBytes struct {
		Blob  []byte  `json:"blob"`  // []byte 按 base64 编码为 string
		Fixed [4]byte `json:"fixed"` // [N]byte 编码为 JSON 数组
	}
	schema, err := SchemaFromType[withBytes]()
	require.NoError(t, err)

	assert.Equal(t, "string", schema.Properties["blob"].Type)

	fixed := schema.Properties["fixed"]
	require.Equal(t, "array", fixed.Type)
	require.NotNil(t, fixed.Items)
	assert.Equal(t, "integer", fixed.Items.Type)
}

func TestSchemaFromTypeFlattensEmbedded(t *testing.T) {
	t.Parallel()

	type base struct {
		ID string `json:"id"`
	}
	type derived struct {
		base
		Name string `json:"name"`
	}
	schema, err := SchemaFromType[derived]()
	require.NoError(t, err)

	assert.Equal(t, "string", schema.Properties["id"].Type)
	assert.Equal(t, "string", schema.Properties["name"].Type)
	assert.ElementsMatch(t, []string{"id", "name"}, schema.Required)
}

func TestSchemaFromTypeEmbeddedConflicts(t *testing.T) {
	t.Parallel()

	t.Run("outer field shadows embedded", func(t *testing.T) {
		t.Parallel()
		type base struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		type derived struct {
			base
			Name int `json:"name"` // 外层（depth 0）遮蔽嵌入的 base.Name
		}
		schema, err := SchemaFromType[derived]()
		require.NoError(t, err)

		assert.Equal(t, "integer", schema.Properties["name"].Type)
		assert.Equal(t, "string", schema.Properties["id"].Type)
		assert.ElementsMatch(t, []string{"name", "id"}, schema.Required)
	})

	t.Run("two embedded same name dropped", func(t *testing.T) {
		t.Parallel()
		// 不加 json tag，靠 Go 字段名同名制造同深度冲突，
		// 避免 go vet structtag 把重复 tag 当成疑似错误。
		type a struct {
			X string
		}
		type b struct {
			X string
		}
		type derived struct {
			a
			b
			Y string `json:"y"`
		}
		schema, err := SchemaFromType[derived]()
		require.NoError(t, err)

		// X 在两个嵌入中同处 depth 1 -> 冲突 -> 整体丢弃。
		assert.NotContains(t, schema.Properties, "X")
		assert.Equal(t, "string", schema.Properties["y"].Type)
		assert.ElementsMatch(t, []string{"y"}, schema.Required)
	})
}

func TestSchemaFromTypeUnsupported(t *testing.T) {
	t.Parallel()

	t.Run("interface field", func(t *testing.T) {
		t.Parallel()
		type bad struct {
			X any `json:"x"`
		}
		_, err := SchemaFromType[bad]()
		require.Error(t, err)
	})

	t.Run("channel field", func(t *testing.T) {
		t.Parallel()
		type bad struct {
			C chan int `json:"c"`
		}
		_, err := SchemaFromType[bad]()
		require.Error(t, err)
	})

	t.Run("non-string map key", func(t *testing.T) {
		t.Parallel()
		type bad struct {
			M map[int]string `json:"m"`
		}
		_, err := SchemaFromType[bad]()
		require.Error(t, err)
	})

	t.Run("unsupported map value type", func(t *testing.T) {
		t.Parallel()
		type bad struct {
			M map[string]chan int `json:"m"`
		}
		_, err := SchemaFromType[bad]()
		require.Error(t, err)
	})

	t.Run("interface map value type", func(t *testing.T) {
		t.Parallel()
		type bad struct {
			M map[string]any `json:"m"`
		}
		_, err := SchemaFromType[bad]()
		require.Error(t, err)
	})

	t.Run("recursive type", func(t *testing.T) {
		t.Parallel()
		type node struct {
			Next *node `json:"next"`
		}
		_, err := SchemaFromType[node]()
		require.Error(t, err)
	})
}

func TestSchemaFromTypeTaggedFieldWins(t *testing.T) {
	t.Parallel()

	// 同深度同名：带 json tag 名的字段优先于靠字段名命中的字段（对齐 encoding/json）。
	type out struct {
		Foo string
		Bar int `json:"Foo"`
	}
	schema, err := SchemaFromType[out]()
	require.NoError(t, err)

	assert.Equal(t, "integer", schema.Properties["Foo"].Type)
	assert.ElementsMatch(t, []string{"Foo"}, schema.Required)
}

func TestJSONSchemaFormatFor(t *testing.T) {
	t.Parallel()

	type out struct {
		City string `json:"city"`
	}
	format, err := JSONSchemaFormatFor[out]("")
	require.NoError(t, err)
	assert.Equal(t, ResponseFormatJSONSchema, format.Type)
	assert.Equal(t, "out", format.Name)

	schema, ok := format.Schema.(ParamSchema)
	require.True(t, ok)
	assert.Equal(t, "object", schema.Type)

	// 指针类型默认名解引用后取底层类型名，而非回退到 "schema"。
	ptr, err := JSONSchemaFormatFor[*out]("")
	require.NoError(t, err)
	assert.Equal(t, "out", ptr.Name)

	named, err := JSONSchemaFormatFor[out]("weather")
	require.NoError(t, err)
	assert.Equal(t, "weather", named.Name)
}

func TestGenerateJSONWithSchema(t *testing.T) {
	t.Parallel()

	type out struct {
		City string `json:"city"`
		Temp int    `json:"temp"`
	}

	var seen *ChatRequest
	p := &stubProvider{
		name: ProviderOpenAI,
		chat: func(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
			seen = req
			return &ChatResponse{Content: `{"city":"杭州","temp":27}`}, nil
		},
	}

	got, resp, err := GenerateJSONWithSchema[out](context.Background(), p, &ChatRequest{
		Messages: []Message{UserText("天气")},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, "杭州", got.City)
	assert.Equal(t, 27, got.Temp)

	require.NotNil(t, seen)
	require.NotNil(t, seen.ResponseFormat)
	assert.Equal(t, ResponseFormatJSONSchema, seen.ResponseFormat.Type)
}

func TestGenerateJSONWithSchemaErrors(t *testing.T) {
	t.Parallel()

	p := &stubProvider{name: ProviderOpenAI}

	t.Run("schema build failure short-circuits before Chat", func(t *testing.T) {
		t.Parallel()
		type bad struct {
			X any `json:"x"`
		}
		_, _, err := GenerateJSONWithSchema[bad](context.Background(), p, &ChatRequest{})
		require.Error(t, err)
	})

	t.Run("nil provider", func(t *testing.T) {
		t.Parallel()
		type out struct {
			City string `json:"city"`
		}
		_, _, err := GenerateJSONWithSchema[out](context.Background(), nil, &ChatRequest{})
		require.ErrorIs(t, err, ErrNilProvider)
	})

	t.Run("nil request", func(t *testing.T) {
		t.Parallel()
		type out struct {
			City string `json:"city"`
		}
		_, _, err := GenerateJSONWithSchema[out](context.Background(), p, nil)
		require.ErrorIs(t, err, ErrNilChatRequest)
	})
}
