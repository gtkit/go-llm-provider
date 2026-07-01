package provider

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

var timeType = reflect.TypeFor[time.Time]()

// SchemaFromType 通过反射为类型 T 生成 ParamSchema（JSON Schema）。
//
// 覆盖范围（OpenAI 兼容结构化输出的常用子集）：
//   - 结构体 -> object，properties 取自导出字段，additionalProperties 固定为 false
//   - string / bool / 整型 / 浮点 -> string / boolean / integer / number
//   - 切片、数组 -> array，items 为元素类型；仅 []byte（切片）映射为 string，[N]byte 仍为 array
//   - map（仅 string 键）-> object，additionalProperties 为 true；值类型仍做受支持校验，但不写入 schema
//   - time.Time -> string（模型返回 RFC3339 字符串，可被 time.Time 解码）
//   - 匿名嵌入的结构体（无 json tag）按 encoding/json 规则提升字段：同名时浅层胜出，最浅深度同名冲突则丢弃该字段
//
// 字段规则：
//   - 读取 json tag 决定字段名；json:"-" 跳过
//   - 指针字段或带 omitempty 的字段视为可选，不进入 required
//   - jsonschema:"enum=a|b|c" 标签为 string 字段生成枚举
//
// 不支持的类型（interface、chan、func、复数、非 string 键的 map、自引用类型）返回错误，
// 由调用方在构造请求前显式处理，不静默降级。
func SchemaFromType[T any]() (ParamSchema, error) {
	return schemaForType(reflect.TypeFor[T](), nil)
}

func schemaForType(t reflect.Type, stack []reflect.Type) (ParamSchema, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == timeType {
		return ParamSchema{Type: "string"}, nil
	}

	switch t.Kind() {
	case reflect.Bool:
		return ParamSchema{Type: "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return ParamSchema{Type: "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return ParamSchema{Type: "number"}, nil
	case reflect.String:
		return ParamSchema{Type: "string"}, nil
	case reflect.Slice, reflect.Array:
		// 仅 []byte（切片）按 JSON 习惯编码为 base64 字符串；
		// [N]byte（定长数组）会被编码/解码为 JSON 数组，仍走 array。
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return ParamSchema{Type: "string"}, nil
		}
		item, err := schemaForType(t.Elem(), stack)
		if err != nil {
			return ParamSchema{}, err
		}
		return ParamSchema{Type: "array", Items: &item}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return ParamSchema{}, fmt.Errorf("schema: 不支持的 map 键类型 %s（仅支持 string 键）", t.Key())
		}
		// 值类型仍按统一约定做受支持校验（interface、chan、func 等会报错，不被绕过），
		// 但 ParamSchema 的 AdditionalProperties 为 *bool，无法承载子 schema，
		// 故值类型不写入 schema，仅以 additionalProperties: true 表达开放对象。
		if _, err := schemaForType(t.Elem(), stack); err != nil {
			return ParamSchema{}, fmt.Errorf("map 值类型: %w", err)
		}
		allow := true
		return ParamSchema{Type: "object", AdditionalProperties: &allow}, nil
	case reflect.Struct:
		return schemaForStruct(t, stack)
	default:
		return ParamSchema{}, fmt.Errorf("schema: 不支持的类型 %s（kind %s）", t, t.Kind())
	}
}

// fieldEntry 是收集阶段的一个候选字段，depth 记录嵌入深度、tagged 记录字段名是否来自
// json tag，用于按 encoding/json 的字段选择规则解决同名冲突。
type fieldEntry struct {
	name     string
	depth    int
	optional bool
	tagged   bool
	schema   ParamSchema
}

func schemaForStruct(t reflect.Type, stack []reflect.Type) (ParamSchema, error) {
	entries, err := collectStructFields(t, stack, 0)
	if err != nil {
		return ParamSchema{}, err
	}
	return buildObjectSchema(entries), nil
}

// collectStructFields 收集结构体（含匿名嵌入提升）的候选字段。
// 匿名嵌入的处理须在非导出跳过之前——未导出嵌入结构体的导出字段同样会被提升。
func collectStructFields(t reflect.Type, stack []reflect.Type, depth int) ([]fieldEntry, error) {
	if slices.Contains(stack, t) {
		return nil, fmt.Errorf("schema: 不支持自引用类型 %s", t)
	}
	stack = append(stack, t)

	var entries []fieldEntry
	for i := range t.NumField() {
		field := t.Field(i)
		name, omitempty, tagged, skip := parseJSONFieldTag(field)
		if skip {
			continue
		}

		if field.Anonymous && name == "" {
			if sub, ok, err := collectEmbedded(field, stack, depth); err != nil {
				return nil, err
			} else if ok {
				entries = append(entries, sub...)
				continue
			}
		}

		if field.PkgPath != "" { // 非导出字段（且非可提升嵌入）
			continue
		}
		if name == "" {
			name = field.Name
		}
		fieldSchema, err := schemaForType(field.Type, stack)
		if err != nil {
			return nil, fmt.Errorf("字段 %s: %w", field.Name, err)
		}
		if enum := parseEnumTag(field); len(enum) > 0 && fieldSchema.Type == "string" {
			fieldSchema.Enum = enum
		}
		entries = append(entries, fieldEntry{
			name:     name,
			depth:    depth,
			optional: omitempty || field.Type.Kind() == reflect.Pointer,
			tagged:   tagged,
			schema:   fieldSchema,
		})
	}
	return entries, nil
}

// collectEmbedded 处理匿名嵌入字段：底层是结构体时按 depth+1 提升其字段；
// ok 为 false 表示不是可提升的嵌入结构体，调用方按普通字段处理。
func collectEmbedded(field reflect.StructField, stack []reflect.Type, depth int) ([]fieldEntry, bool, error) {
	ft := field.Type
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if ft.Kind() != reflect.Struct || ft == timeType {
		return nil, false, nil
	}
	sub, err := collectStructFields(ft, stack, depth+1)
	if err != nil {
		return nil, false, err
	}
	return sub, true, nil
}

// buildObjectSchema 按 encoding/json 的字段选择规则解决同名冲突，详见 dominantField。
func buildObjectSchema(entries []fieldEntry) ParamSchema {
	byName := make(map[string][]fieldEntry, len(entries))
	order := make([]string, 0, len(entries))
	for _, e := range entries {
		if _, ok := byName[e.name]; !ok {
			order = append(order, e.name)
		}
		byName[e.name] = append(byName[e.name], e)
	}

	disallow := false
	schema := ParamSchema{
		Type:                 "object",
		Properties:           map[string]ParamSchema{},
		AdditionalProperties: &disallow,
	}
	for _, name := range order {
		winner, ok := dominantField(byName[name])
		if !ok {
			continue
		}
		schema.Properties[name] = winner.schema
		if !winner.optional {
			schema.Required = append(schema.Required, name)
		}
	}
	return schema
}

// dominantField 在同名字段集合中按 encoding/json 规则选出胜出者：
//   - 深度最浅者优先；
//   - 同处最浅深度时，唯一带 json tag 名的字段胜出；
//   - 若最浅深度上无 tag 字段且不止一个，或存在多个 tag 字段，则无胜出者（字段整体丢弃）。
//
// group 至少含一个元素。
func dominantField(group []fieldEntry) (fieldEntry, bool) {
	minDepth := group[0].depth
	for _, e := range group[1:] {
		minDepth = min(minDepth, e.depth)
	}

	var atMin, tagged []fieldEntry
	for _, e := range group {
		if e.depth != minDepth {
			continue
		}
		atMin = append(atMin, e)
		if e.tagged {
			tagged = append(tagged, e)
		}
	}

	if len(atMin) == 1 {
		return atMin[0], true
	}
	if len(tagged) == 1 {
		return tagged[0], true
	}
	return fieldEntry{}, false
}

// parseJSONFieldTag 解析 json tag。tagged 表示字段名来自 tag（用于同名冲突时的优先级判定）；
// json:",omitempty" 这类空名称不算 tagged。
func parseJSONFieldTag(field reflect.StructField) (name string, omitempty, tagged, skip bool) {
	tag := field.Tag.Get("json")
	if tag == "" {
		return "", false, false, false
	}
	if tag == "-" {
		return "", false, false, true
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	tagged = name != ""
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, tagged, false
}

func parseEnumTag(field reflect.StructField) []string {
	tag := field.Tag.Get("jsonschema")
	if tag == "" {
		return nil
	}
	for part := range strings.SplitSeq(tag, ",") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "enum="); ok {
			return strings.Split(v, "|")
		}
	}
	return nil
}

// JSONSchemaFormatFor 基于类型 T 反射生成 json_schema 形式的 ResponseFormat。
// name 为空时取 T 的类型名，匿名类型回退为 "schema"。
//
// 生成的是非 strict 的 json_schema（Strict 未设置）。OpenAI strict 模式要求每个属性都进
// required，并以 nullable union（type: [..., "null"]）表达可选字段，而 ParamSchema 的 Type
// 为单一字符串、无法表达 union，故默认非 strict。若你的类型所有字段都必填且需要 strict，
// 可用 JSONSchemaFormatStrict(name, schema) 组合 SchemaFromType[T]() 的结果。
func JSONSchemaFormatFor[T any](name string) (*ResponseFormat, error) {
	schema, err := SchemaFromType[T]()
	if err != nil {
		return nil, fmt.Errorf("schema from type: %w", err)
	}
	if name == "" {
		rt := reflect.TypeFor[T]()
		for rt.Kind() == reflect.Pointer {
			rt = rt.Elem()
		}
		if n := rt.Name(); n != "" {
			name = n
		} else {
			name = "schema"
		}
	}
	return JSONSchemaFormat(name, schema), nil
}

// GenerateJSONWithSchema 在请求未显式设置 ResponseFormat 时，
// 自动注入由 T 反射生成的 json_schema，再调用 Chat 并将响应解码为 T。
func GenerateJSONWithSchema[T any](ctx context.Context, p Provider, req *ChatRequest) (T, *ChatResponse, error) {
	return GenerateJSONWithSchemaValidator[T](ctx, p, req, nil)
}

// GenerateJSONWithSchemaValidator 与 GenerateJSONWithSchema 相同，
// 并在解码后运行 validator（非 nil 时）。
func GenerateJSONWithSchemaValidator[T any](
	ctx context.Context,
	p Provider,
	req *ChatRequest,
	validator StructuredValidator[T],
) (T, *ChatResponse, error) {
	var zero T
	if providerIsNil(p) {
		return zero, nil, ErrNilProvider
	}
	if req == nil {
		return zero, nil, ErrNilChatRequest
	}

	next := *req
	if next.ResponseFormat == nil {
		format, err := JSONSchemaFormatFor[T]("")
		if err != nil {
			return zero, nil, err
		}
		next.ResponseFormat = format
	}
	return GenerateJSONWithValidator(ctx, p, &next, validator)
}
