package kb

import (
	"os"
	"path/filepath"
	"testing"
)

// repoCorpusDir 定位集成测试用的**真实语料**目录。
//
// ★ 语料默认是仓库根的 docs/ —— heron 侧那批中文设计文档。
//
//	openheron 这个发行版**不含 docs/**：它只带代码面（Go 后端 + web 前端 +
//	构建配置）。所以这里的默认值在 openheron 里通常不存在。
//
// ★ 为什么是「跳过并说清楚」而不是「响亮地失败」：
//
//	上游的约定是「语料路径变了要响亮地失败」—— 那针对的是**有人挪了语料**
//	而没人发现：集成测试静默失效等于不存在。但 openheron 里语料是**按设计**
//	缺席的（发行版不含 docs/），不是被谁挪走了。这两种情况必须分开，否则
//	openheron 的 `go test ./...` 会永远红着，而红的原因与代码无关。
//
//	★ 折中点：**目录存在但没有 md** 仍然响亮地失败 —— 那才是「语料被挪了/
//	  被清空了」。只有「目录整个不在」才跳过。
//
// ★ 用 OPENHERON_KB_CORPUS 可以指到别的语料根 —— 例如把上游 docs/ 放到
//
//	仓库外做回归对照，或用另一批真实文档验抽取与召回。
func repoCorpusDir(t *testing.T) string {
	t.Helper()

	dir := os.Getenv("OPENHERON_KB_CORPUS")
	if dir == "" {
		dir = filepath.Join("..", "docs")
	}

	_, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		t.Skipf("语料目录 %s 不存在：openheron 发行版不含 docs/，这是按设计的缺席，"+
			"不是语料被挪走。用 OPENHERON_KB_CORPUS 指向一个语料根即可让这条用例跑起来。",
			dir)
	case err != nil:
		t.Fatalf("stat corpus %s: %v", dir, err)
	}

	return dir
}
