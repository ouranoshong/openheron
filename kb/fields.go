package kb

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

const (
	// FieldContent 是正文字段名。它同时是 Redis 版的默认 EmbedKey，
	// 两侧同字面量 —— 换后端时 ReturnFields / VectorField 的默认值不用改。
	FieldContent = "content"
	// FieldVector 是向量字段名，对应 Redis 版的 defaultReturnFieldVectorContent。
	FieldVector = "vector_content"

	defaultBatchSize = 10
	defaultTopK      = 5
	// defaultMaxBatchChars 是一批向量化文本的字符数上限。
	//
	// ★ 1200 是按「1 字符 ≈ 1 token」的**保守**估计定的：中文几乎 1:1，
	//   英文约 1:4，所以对中文刚好、对英文过激。而过激的代价只是多几轮
	//   请求（llama-server 单槽，小批之间的开销很小），撑爆的代价是
	//   整个进程挂掉。
	defaultMaxBatchChars = 1200
)

// Fields 是一个文档在建索引时如何落盘的描述，与 Redis 版的 Hashes 同构。
//
// ★ 名字从 Hashes 改成 Fields 的唯一原因是 Redis 版的 Hashes 这个词把
//
//	「hash 结构」和「字段集合」混在一起了 —— 本包不存 hash。
//	**结构本身完全一致**，否则装配层那 3 行就抄不过来了。
//
// ★ **v2 的文件实现不读它** —— 索引走的是 [Entry] 与目录（catalog.md），
//
//	不是字段集合。这个类型与 [DocumentToFields] 是**留口**：
//	本仓不做 Redis 后端（docs/kb-design.md §1），但将来若接，
//	索引器的配置面要能与 Redis 版**逐字段对齐** —— 那个「3 行就抄过来」
//	的前提就是形状一致。
type Fields struct {
	// Key 是主键。用 document ID。
	Key string
	// Field2Value 是字段名 → 取值。
	Field2Value map[string]FieldValue
}

// FieldValue 是一个字段的取值，以及「它是否要被向量化」。
type FieldValue struct {
	// Value 原始值。
	Value any
	// EmbedKey 非空 = 这个字段要向量化，存到该名字下。
	//
	// ★ 它是**字段名**而不是「要不要 embed」的布尔 —— 存与查必须用同一个
	//   名字，写入端和检索端各报一次就不会错位。布尔会让两边各猜一次。
	EmbedKey string
	// Stringify 把 Value 转成送进 embedder 的文本。
	// 为 nil 时按 string 断言，断言失败即报错。
	Stringify func(val any) (string, error)
}

// DocumentToFields 把文档变成 Fields。调用方可在 IndexerConfig 里替换。
//
// ★ **v2 的文件索引器不调用它** —— 见 [Fields] 的说明。这个类型是留口。
type DocumentToFields func(ctx context.Context, doc *schema.Document) (*Fields, error)
