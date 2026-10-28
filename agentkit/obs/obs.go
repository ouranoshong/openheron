// Package obs 是观测的入口：进程级日志器与本轮标识（runId）。
//
// ★ 为什么它是叶子包：观测是**旁路** —— 关掉它，主流程一点不变。
//
//	所以它不许认识任何 openheron 概念（会话、agent、配置、知识库），
//	只依赖标准库。谁想被观测，就自己往回调里发事件。
//
// ★ 它**不含 eino**：日志入口每个业务包都要用，而 config 的依赖闭包里
//
//	eino 的包数必须是 0（docs/architecture.md §3.2.1）。收 eino 回调的那半
//	在 [openheron/obs/hook] 里 —— 那才是唯一依赖 eino 的地方。
package obs
