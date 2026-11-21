// Package safestr 提供按 UTF-8 字符边界的安全截断（codex 深审 P2 修复）：
// 按字节硬切会把多字节字符截半，落库成非法 UTF-8（快照/错误文案可含中文）；
// 而全量回退到字符边界又会把「整体非法」的输入（二进制体）清成空串。
// 三条口径：Head 前缀截断、Tail 后缀截断、HeadEllipsis 带省略号前缀截断。
package safestr

import "unicode/utf8"

// Head 取 s 的前缀至多 n 字节，截点回退到字符边界。n<=0 或 s 未超限时
// 语义见实现：未超限原样返回；n<=0 返回空串。输入整体为非法 UTF-8（如
// 二进制体）时回退至多 utf8.UTFMax-1 字节即止损——不再回退会让快照
// 完全丢失成空串。
func Head(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for i := 0; i < utf8.UTFMax-1 && n > 0 && !utf8.RuneStart(s[n]); i++ {
		n--
	}
	return s[:n]
}

// Tail 取 s 的后缀至多 n 字节，起点前移到字符边界（错误摘要常取尾段，
// 首字节可能是多字节字符的后半）。非法 UTF-8 输入至多前移 utf8.UTFMax-1
// 字节止损，不会把尾段清空。
func Tail(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for i := 0; i < utf8.UTFMax-1 && start < len(s) && !utf8.RuneStart(s[start]); i++ {
		start++
	}
	return s[start:]
}

// HeadEllipsis 取 s 的前缀至多 n 字节并追加省略号，截点回退到字符边界
// （DNS 商错误摘要口径：截断产物以 … 结尾示意有省略）。
func HeadEllipsis(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return Head(s, n) + "…"
}
