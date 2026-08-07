// Package prompt 装配系统提示。
//
// 唯一的硬要求是输出逐字节稳定：同一份输入两次装配必须完全一致，
// 否则供应商侧的 prompt 缓存每轮都会失效，成本按倍数上涨。
// 因此这里不得出现时间戳、随机序、map 遍历顺序。
package prompt

import "strings"

// Builder 按声明顺序拼接系统提示的各段。
type Builder struct {
	base     string
	sections []section
}

type section struct {
	heading string
	body    string
}

// New 返回以 base 为首段的 Builder。
func New(base string) *Builder {
	return &Builder{base: base}
}

// Section 追加一段。body 为空或只含空白时整段省略 ——
// 空标题会让模型以为有内容却看不到，是纯噪声。
func (b *Builder) Section(heading, body string) *Builder {
	if strings.TrimSpace(body) == "" {
		return b
	}
	b.sections = append(b.sections, section{heading: heading, body: body})
	return b
}

// Build 返回装配好的系统提示。
func (b *Builder) Build() string {
	var sb strings.Builder

	if base := strings.TrimSpace(b.base); base != "" {
		sb.WriteString(base)
	}

	for _, s := range b.sections {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("## ")
		sb.WriteString(s.heading)
		sb.WriteString("\n\n")
		sb.WriteString(strings.TrimSpace(s.body))
	}

	return sb.String()
}
