// Package kb 是本地向量知识库的存储层。
//
// ★ 它是叶子包：只 import eino / eino-ext / agentkit/run，不 import 任何业务包。
//
// 设计见 docs/kb-design.md。
//
// # 为什么字段名与 Redis 版一字不差
//
// [FileIndexerConfig] / [FileRetrieverConfig] 逐字段镜像 eino-ext 的
// RedisIndexer / RedisRetriever —— 名字、缺省值、连 `DistanceThreshold`
// 是指针（nil 有语义）都对齐。**这不是巧合，是「将来换后端只改装配的 3 行」
// 这条收益的兑现方式**：两层都是接口时，换后端不碰流水线；但只有字段名也
// 对齐，装配层那 3 行才真的抄得过来。
//
// 对不齐的三处（`Dialect`、`ImplOptions.FilterQuery`、DocumentConverter 的
// 参数类型）在 docs/kb-design.md §6.1 有理由，本包不假装它们存在。
//
// # 已知与 Redis 版的行为差异
//
// 落盘用 gob，只注册了几种基本类型。**未注册的值类型会在写入时报错**，
// 而不会被悄悄转成别的 —— 换 Redis 能拿到更宽的类型面，但那不是本包要
// 解决的问题。
package kb
