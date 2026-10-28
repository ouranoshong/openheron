package obs

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

// LevelEnv 是控制日志级别的环境变量。
//
// ★ 默认 info：debug 会把每个图节点、每次工具调用都打出来，
//
//	那是排查问题时才要开的档位。
const LevelEnv = "OPENHERON_LOG_LEVEL"

var (
	loggerMu sync.RWMutex
	logger   *slog.Logger
)

// Logger 返回进程级日志器。
//
// ★ 它是进程级的，因为观测是**旁路**：它必须在任何地方都能被取到，
//
//	而「取到」的方式如果依赖注入，那么每一个业务包都要多一个字段
//	—— 那不是旁路，那是把观测接进了业务。
func Logger() *slog.Logger {
	loggerMu.RLock()
	l := logger
	loggerMu.RUnlock()
	if l != nil {
		return l
	}
	// 未初始化时给一个可用的默认值：写 stderr、级别取自环境变量。
	l = DefaultLogger(os.Stderr)
	loggerMu.Lock()
	logger = l
	loggerMu.Unlock()
	return l
}

// SetLogger 替换进程级日志器。
//
// ★ 只在启动时调用一次（main）。运行期改它会让同一轮的前半段与
//
//	后半段用不同的日志器，日志对不上。
func SetLogger(l *slog.Logger) {
	if l == nil {
		return
	}
	loggerMu.Lock()
	logger = l
	loggerMu.Unlock()
}

// DefaultLogger 建一个写文本日志的 logger。
//
// ★ 文本而不是 JSON：日志是给人看的（排障时 tail -f），
//
//	JSON 要机器看，那是采集端的事。
func DefaultLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: levelFromEnv()}))
}

// levelFromEnv 读环境变量里的级别。
//
// ★ 认不出就当 info —— 宁可少记，也不要因为一个拼错的变量名
//
//	把日志全关掉（那会让「日志没了」这件事本身无从察觉）。
func levelFromEnv() slog.Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(LevelEnv))) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
