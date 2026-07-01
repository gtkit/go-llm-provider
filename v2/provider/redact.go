package provider

// MaskSecret 对密钥类字符串脱敏：保留首尾少量字符、中间固定打码，
// 便于在日志中保留可追溯性而不暴露完整值（如 "sk-1234...wxyz" -> "sk-1****wxyz"）。
//
// 本库自身不会向调用方回显 API Key（响应头按白名单过滤，请求不含密钥），
// 该函数供调用方在自己的日志、追踪、错误上报中给密钥打码使用。
//
// 规则：
//   - 空串返回空串
//   - 长度 <= 8 的短串整体打码为 "****"，不泄露长度
//   - 其余保留前 4 位与后 4 位，中间固定替换为 "****"
func MaskSecret(s string) string {
	const stars = "****"
	switch n := len(s); {
	case n == 0:
		return ""
	case n <= 8:
		return stars
	default:
		return s[:4] + stars + s[n-4:]
	}
}
