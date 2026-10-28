package persist

import (
	"fmt"
	"os"
	"path/filepath"
)

// ★ 本包是**文件持久化原语**的唯一产地：原子写与跨进程锁。
//
//	两件事的用途完全不同：checkpoint 要的是「半个 gob 读不出来」不能发生，
//	配置要的是「读者永远看不到写了一半的 YAML」。但要保证的其实是同一件事
//	—— 目标文件要么是旧内容、要么是新内容，没有第三种。
//	各写一份必然漂：某天有人只改一处（比如忘了 fsync），而那个差异
//	只在断电时显形，且极难复现。
//
// ★ 本包是**叶子**：不 import 任何 openheron 包，也不含任何运行时概念。
//
//	这不是洁癖，是依赖方向的硬要求。[config] 与 [kb] 都需要落盘原语，
//	而它们分别位于配置层与知识库层 —— 如果原语住在运行时的 [agentkit/run] 里，
//	这两个包就都被迫依赖整个运行时包，依赖铁律「agent 不认 agentkit/run」会被
//	从传递路径上悄悄破坏（agent → config → agentkit/run）。
//	把它们放进一个叶子包，反向依赖就不存在了。
//
//	（这条依赖方向是铁律，见 architecture.md §3.1 与 config-design.md §9。）

// AtomicWrite 把 data 原子地写到 dst：写 dst+tmp → fsync → rename。
//
// ★ rename 在 POSIX 上是原子的，所以读者要么看到旧内容、要么看到新内容，
//
//	永远不会看到截断的中间态。对 checkpoint 尤其要紧：半个 gob 解不出来，
//	而 Runtime 会在同一位置反复失败（见 checkpoint-store.md §6.2）。
//
// tmpSuffix 由调用方决定，不是实现能替它定的：临时文件名要能被**该目录的
// 加载逻辑忽略掉**（否则残留的 .tmp 会被当成一个实体文件读进来），
// 也要在进程中途死掉时一眼认出是残留。
func AtomicWrite(dst string, data []byte, tmpSuffix string) error {
	if tmpSuffix == "" {
		// 没有后缀的话临时文件就与目标同名，rename 会变成自己覆盖自己。
		return fmt.Errorf("persist: empty tmp suffix for %s", dst)
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("persist: create %s: %w", dir, err)
	}

	tmp := dst + tmpSuffix
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("persist: create %s: %w", tmp, err)
	}

	// 失败路径统一收口：关掉句柄并删掉临时文件，绝不留半个文件在盘上。
	// 留着的话下一次同名写入会 O_TRUNC 覆盖掉，所以不是致命问题 ——
	// 但目录里堆着残留 tmp 会让人以为「保存过一次」。
	cleanup := func(err error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}

	if _, err := f.Write(data); err != nil {
		return cleanup(fmt.Errorf("persist: write %s: %w", tmp, err))
	}
	// ★ fsync 的是**数据**。不 fsync 的话内容可能还在页缓存里，
	// 而 rename 已经把名字换掉了 —— 崩溃后得到一个「存在但空」的目标。
	if err := f.Sync(); err != nil {
		return cleanup(fmt.Errorf("persist: sync %s: %w", tmp, err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: close %s: %w", tmp, err)
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("persist: rename %s -> %s: %w", tmp, dst, err)
	}

	// ★ 目录也要 fsync：rename 本身是一次目录项改动，不刷目录的话
	//   崩溃后可能看不到这个新名字。
	//
	//   原先的 checkpoint 实现只 fsync 了文件，这里一并补上 ——
	//   对 checkpoint 是纯粹的增强，对配置无影响（重读一遍即可）。
	syncDir(dir)
	return nil
}

// syncDir 刷目录项。失败**不上报**。
//
// ★ 理由：它只在断电场景下有意义，而那时的正确处置是「重扫目录」，
//
//	配置文件可以重新读、checkpoint 会被当成「不存在」而重新生成。
//	为此让一次正常保存失败，是把一个只在断电时才发生的问题，
//	换成了每次保存都可能发生的问题。
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
