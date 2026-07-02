package storage_lock_factory

import (
	"context"
	"github.com/stretchr/testify/assert"
	"sync"
	"sync/atomic"
	"testing"
)

const testKey = "test"

func TestStorageLockFactoryBeanFactory_GetBean(t *testing.T) {

	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	// 未初始化时应该获取不到
	bean, b := beanFactory.GetBean(testKey)
	assert.False(t, b)
	assert.Nil(t, bean)

	// 初始化
	beanFactory.InitBean(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})

	// 初始化完之后获取到了
	bean, b = beanFactory.GetBean(testKey)
	assert.True(t, b)
	assert.NotNil(t, bean)

}

func TestStorageLockFactoryBeanFactory_GetOrInit(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	// 第一次获取
	factory1, err := beanFactory.GetOrInit(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})
	assert.Nil(t, err)
	assert.NotNil(t, factory1)

	// 第二次获取
	factory2, err := beanFactory.GetOrInit(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})
	assert.Nil(t, err)
	assert.NotNil(t, factory2)

	// 期望两次获取到的应该是同一个值
	assert.Equal(t, factory1, factory2)

}

func TestStorageLockFactoryBeanFactory_InitBean(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	// 未初始化时应该获取不到
	bean, b := beanFactory.GetBean(testKey)
	assert.False(t, b)
	assert.Nil(t, bean)

	// 初始化
	beanFactory.InitBean(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})

	// 初始化完之后获取到了
	bean, b = beanFactory.GetBean(testKey)
	assert.True(t, b)
	assert.NotNil(t, bean)

}

// TestBeanFactoryConcurrentInitNoCrash 钉死漏洞 L：并发 GetOrInit 同一 key 不能崩溃、
// initFunc 恰好被调用一次（无重复初始化/资源泄漏）。
// 修复前 InitBean 的 Lock();Unlock() 立即释放锁，map 并发读写会触发 Go 运行时
// fatal error: concurrent map read and map write（不可 recover，进程崩溃），
// 且两 goroutine 都会执行 initFunc、覆盖式写 map，被覆盖的工厂永不 Shutdown。
func TestBeanFactoryConcurrentInitNoCrash(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	var initCount int64
	const n = 50
	var wg sync.WaitGroup
	factories := make([]*StorageLockFactory[string], n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // 同时起跑，最大化并发竞争
			f, err := beanFactory.GetOrInit(context.Background(), "concurrent-key", func(ctx context.Context) (*StorageLockFactory[string], error) {
				atomic.AddInt64(&initCount, 1)
				return NewStorageLockFactory[string](nil, nil), nil
			})
			if err != nil {
				t.Errorf("GetOrInit 不应失败: %v", err)
				return
			}
			factories[idx] = f
		}(i)
	}
	close(start)
	wg.Wait()

	// 核心断言1：initFunc 恰好被调用一次（双重检查锁保证单例，无重复初始化/资源泄漏）
	assert.Equal(t, int64(1), initCount,
		"initFunc 应恰好调用一次，实际 %d（说明并发下重复初始化，被覆盖的工厂资源泄漏）", initCount)

	// 核心断言2：所有 goroutine 拿到的是同一个工厂实例
	first := factories[0]
	for i := 1; i < n; i++ {
		assert.Equal(t, first, factories[i], "所有并发获取应返回同一个单例工厂")
	}

	// 核心断言3：能走到这里说明没有触发 fatal crash（漏洞 L 修复的硬证据）
}

// TestBeanFactoryConcurrentShutdownAllNoCrash 钉死漏洞 L 的 ShutdownAll 路径：
// 并发 ShutdownAll + GetOrInit 不能崩溃。修复前 ShutdownAll 同样 Lock();Unlock() 立即释放，
// 与并发的 InitBean 遍历 map 时触发 concurrent map read and map write。
//
// 用返回 err 的 initFunc 让 bean.Factory==nil，ShutdownAll 走 errorMap 分支不调 Shutdown，
// 聚焦验证"map 并发读写不崩溃"这一漏洞 L 的核心，避免牵涉 StorageLockFactory.Shutdown 的 nil 健壮性（那是另一回事）。
func TestBeanFactoryConcurrentShutdownAllNoCrash(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	// 预置几个 bean（Factory==nil，ShutdownAll 不会调 Shutdown，避免 nil 字段解引用）
	for i := 0; i < 5; i++ {
		key := "shutdown-key-" + string(rune('a'+i))
		_, _ = beanFactory.GetOrInit(context.Background(), key, func(ctx context.Context) (*StorageLockFactory[string], error) {
			return nil, assert.AnError // 返回错误，bean.Factory 为 nil
		})
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	// 一边反复 ShutdownAll，一边反复 GetOrInit 同一/新 key
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = beanFactory.ShutdownAll(context.Background())
		}()
		go func(idx int) {
			defer wg.Done()
			<-start
			_, _ = beanFactory.GetOrInit(context.Background(), "concurrent-shutdown-key", func(ctx context.Context) (*StorageLockFactory[string], error) {
				return nil, assert.AnError
			})
		}(i)
	}
	close(start)
	wg.Wait()
	// 走到这里说明没有 fatal crash（漏洞 L 的 ShutdownAll 路径修复有效）
}

// TestBeanFactoryShutdownRemovesBean 钉死漏洞5：Shutdown 成功后 bean 从 map 移除，
// 后续 GetOrInit 不会命中"已死"工厂，而是重新初始化。
// 修复前 Shutdown 不删 bean，GetOrInit 仍返回已 Close 的工厂，调用方 Lock 走已关 Storage；
// 且 map 永不缩容（资源泄漏）。
func TestBeanFactoryShutdownRemovesBean(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	var initCount int64
	key := "shutdown-remove-key"
	f1, err := beanFactory.GetOrInit(context.Background(), key, func(ctx context.Context) (*StorageLockFactory[string], error) {
		atomic.AddInt64(&initCount, 1)
		return NewStorageLockFactory[string](nil, nil), nil // nil Storage/CM，Shutdown 走 nil 守卫不 panic
	})
	assert.Nil(t, err)
	assert.NotNil(t, f1)
	assert.Equal(t, int64(1), initCount)

	// Shutdown 后该 key 应从 map 移除
	err = beanFactory.Shutdown(context.Background(), key)
	assert.Nil(t, err)

	// 再次 GetOrInit 应重新初始化（initCount=2），而非返回已死的 f1
	f2, err := beanFactory.GetOrInit(context.Background(), key, func(ctx context.Context) (*StorageLockFactory[string], error) {
		atomic.AddInt64(&initCount, 1)
		return NewStorageLockFactory[string](nil, nil), nil
	})
	assert.Nil(t, err)
	assert.Equal(t, int64(2), initCount, "Shutdown 后应重新初始化，而非返回已死工厂（漏洞5）")
	// 两个工厂是不同实例（NewStorageLockFactory 各自 new）
	assert.NotSame(t, f1, f2, "应得到全新工厂实例，而非已 Shutdown 的旧实例")

	// 清理
	_ = beanFactory.Shutdown(context.Background(), key)
}

// TestBeanFactoryShutdownAllClearsMap 钉死漏洞5 的 ShutdownAll 路径：
// ShutdownAll 后 map 应清空，GetOrInit 全部重新初始化。
func TestBeanFactoryShutdownAllClearsMap(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	var initCount int64
	initFn := func(ctx context.Context) (*StorageLockFactory[string], error) {
		atomic.AddInt64(&initCount, 1)
		return NewStorageLockFactory[string](nil, nil), nil
	}
	_, _ = beanFactory.GetOrInit(context.Background(), "k1", initFn)
	_, _ = beanFactory.GetOrInit(context.Background(), "k2", initFn)
	_, _ = beanFactory.GetOrInit(context.Background(), "k3", initFn)
	assert.Equal(t, int64(3), initCount)

	errMap := beanFactory.ShutdownAll(context.Background())
	// 全 nil Storage/CM，ShutdownAll 走 nil 守卫，每个工厂 Shutdown 应返回 nil
	for k, e := range errMap {
		assert.Nil(t, e, "key=%v 的 Shutdown 应返回 nil（nil Storage/CM 走守卫）", k)
	}

	// ShutdownAll 后 map 应清空，再次 GetOrInit 全部重新初始化
	_, _ = beanFactory.GetOrInit(context.Background(), "k1", initFn)
	assert.Equal(t, int64(4), initCount, "ShutdownAll 清空 map 后应重新初始化（漏洞5）")
}
