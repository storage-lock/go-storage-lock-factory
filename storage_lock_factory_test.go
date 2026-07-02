package storage_lock_factory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestStorageLockFactoryShutdownNilSafe 钉死漏洞3：Storage/ConnectionManager 为 nil 时
// Shutdown 不得 panic。go-memory-locks 等不需要连接管理器的场景会传 nil
// （MemoryStorage 无外部资源），修复前 Shutdown 直接 x.Storage.Close / x.ConnectionManager.Shutdown
// 必 nil 解引用 panic（进程级、不可 recover）。
func TestStorageLockFactoryShutdownNilSafe(t *testing.T) {
	// 全 nil：Storage=nil, ConnectionManager=nil（go-memory-locks 的合法用法）
	f := NewStorageLockFactory[any](nil, nil)
	assert.NotPanics(t, func() {
		err := f.Shutdown(context.Background())
		assert.Nil(t, err, "全 nil 时 Shutdown 应无错误返回")
	}, "Storage/ConnectionManager 均为 nil 时 Shutdown 不应 panic（漏洞3）")
}
