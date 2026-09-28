package websearch

import (
	"regexp"
	"strings"
	"unicode"
)

// 搜索结果是彻头彻尾的外部不可信内容：攻击者只要让一个页面被搜到，就能把指令送进
// 你的 prompt（间接提示注入）。本文件是第一道防线"内容净化"的最小可用实现。
//
// 完整的规则模板（更长的特征词表、已验证的攻击载荷与正常文本用例）见仓库根目录的
// PROMPT_INJECTION_DEFENSE.md，与本库解耦、可直接复制。库本身不内置规则：
// 判定什么可疑、命中后是替换还是拒绝，都是业务口径。

// suspiciousPatterns 是可疑指令特征词。
//
// 锚点之间用 .{0,N} 容忍中间插入代词与修饰词：要求锚点严格相邻时，
// "ignore all previous instructions" 与"忘记你之前的处理任务"这类最常见的注入短语都会漏检。
// 放宽窗口会提高误报率，因此命中后只做降级替换 + 审计，不中断整条流程。
var suspiciousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)ignore\s+.{0,25}(instructions?|prompts?|rules?)`),
	regexp.MustCompile(`(?is)(forget|disregard)\s+.{0,25}(previous|prior|above|instructions?)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+a`),
	regexp.MustCompile(`(?i)system\s*:\s*`),
	regexp.MustCompile(`(?i)new\s+instructions?`),
	regexp.MustCompile(`(?s)(忘记|忽略).{0,6}(前面|上面|之前|所有|全部).{0,6}(指令|任务|要求|提示)`),
	regexp.MustCompile(`你现在是`),
	regexp.MustCompile(`请(忘记|忽略)(系统|之前)`),
	regexp.MustCompile(`(?s)新的.{0,4}(指令|任务|角色)`),
	regexp.MustCompile(`系统提示词`),
}

// structuralEscaper 转义可能破坏 prompt 结构的控制符。
// system prompt 常用 Markdown 分节书写，搜索结果原样携带 "##" / "---" 会在视觉上
// 伪造出新的章节，即使外层已有标签隔离也可能干扰模型对结构的理解。
var structuralEscaper = strings.NewReplacer(
	"---", `\-\-\-`,
	"```", "\\`\\`\\`",
	"##", `\#\#`,
	"<|", `\<|`,
	"|>", `|\>`,
)

// SanitizedPlaceholder 是命中可疑特征词后替换进 prompt 的占位符。
const SanitizedPlaceholder = "[SANITIZED: 该来源内容包含疑似注入指令，已被安全策略移除]"

// IsSuspicious 报告文本是否命中可疑指令特征词。匹配在规范化副本上进行（见 normalizeForDetection），
// 全角字母、零宽字符、NBSP 打散的关键词同样命中。
func IsSuspicious(text string) bool {
	normalized := normalizeForDetection(text)
	for _, pattern := range suspiciousPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

// normalizeForDetection 生成只用于特征匹配的副本：全角 ASCII 折为半角、删除零宽等格式字符、
// 各类空白（NBSP、全角空格等）归一为空格。原文不做改写，避免误伤正常内容的排版。
func normalizeForDetection(text string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			return r - 0xFEE0
		case unicode.Is(unicode.Cf, r):
			return -1
		case unicode.IsSpace(r):
			return ' '
		}
		return r
	}, text)
}

// SanitizeText 是默认的内容净化：命中特征词整段替换为占位符，否则转义结构符。
// 长度截断由调用方按 token 预算控制（见 HandlerOptions.MaxTextLen），不在这里做。
func SanitizeText(text string) string {
	if IsSuspicious(text) {
		return SanitizedPlaceholder
	}
	return structuralEscaper.Replace(text)
}

// truncateRunes 按字符（而非字节）截断，避免把多字节字符切成乱码。
func truncateRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return text
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "…"
}
