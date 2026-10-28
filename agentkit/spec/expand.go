package spec

import (
	"fmt"
	"os"
	"strings"
)

// expandValue 展开取值里的两种引用。
//
//	${ENV}      读环境变量
//	@file:path  读文件（密钥盘 / 容器 secret）
//
// 第二个返回值 resolved 表示**是否成功取到了值**。它为 false 只有一种情形：
// `${ENV}` 指向的环境变量没设置 —— 这是**提醒**，不是错误。
//
// ★ 为什么缺 env 不致命：密钥常在运行时才注入（容器 / CI / 本地 shell），
// 「声明了 provider 但此刻还没密钥」不该让整个进程起不来。真正该失败的是
// **调用该端点**那一刻 —— 空 key 会让端点在请求内明确回 401，协议层归成
// auth 错误并给出「检查 api_key」的提示（见 llm/common 的 KindAuth）。
// 把失败点从「启动」移到「请求」，既不误伤未用到的 provider，又保留了
// 「迟早要修」这个事实的可见性。
//
// `@file:` 读不到文件仍是**硬错误**：那是路径写错，不是「暂时没注入」。
func expandValue(v string) (value string, resolved bool, err error) {
	v = strings.TrimSpace(v)

	switch {
	case strings.HasPrefix(v, "@file:"):
		p := strings.TrimSpace(strings.TrimPrefix(v, "@file:"))
		if p == "" {
			return "", false, fmt.Errorf("@file: missing path after the prefix")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", false, fmt.Errorf("read secret file: %w", err)
		}
		return strings.TrimSpace(string(b)), true, nil

	case strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}"):
		name := strings.TrimSpace(v[2 : len(v)-1])
		if name == "" {
			return "", false, fmt.Errorf("${} is missing a variable name")
		}
		val, ok := os.LookupEnv(name)
		if !ok {
			// ★ 未设置 ⇒ 空值 + resolved=false，由调用方记一条启动提醒。
			return "", false, nil
		}
		return strings.TrimSpace(val), true, nil

	default:
		return v, true, nil
	}
}

// envVarName 取出 `${ENV}` 里的变量名；不是该形态时返回空串。
// 供 loadProvider 生成「变量 X 未设置」的提醒文案。
func envVarName(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		return strings.TrimSpace(v[2 : len(v)-1])
	}
	return ""
}

// splitFrontmatter 切出 `---` 包裹的 frontmatter 与正文。
//
// eino 的 parseFrontmatter 是**未导出**的（adk/middlewares/skill/
// filesystem_backend.go），无法复用，自写一份。
//
// ★ **正文必须原样保留**：它就是 Instruction，frontmatter 之外的任何字符
//
//	（包括看似是元信息的）都进提示词。
func splitFrontmatter(data []byte) ([]byte, string, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, "", nil
	}
	if !strings.HasPrefix(text, "---") {
		// 没有 frontmatter 的 .md 视为**空声明**而非错误 ——
		// 它可能是纯提示词草稿。但仍会因缺 model 在后续校验里报错。
		return nil, text, nil
	}

	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter closing delimiter not found")
	}
	fm := rest[:end]
	body := strings.TrimLeft(rest[end+4:], "\n")
	return []byte(fm), body, nil
}
