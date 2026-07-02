package storage_lock_factory

import (
	"context"
	"fmt"
	"sync"
)

// InitFunc 用于初始化StorageLockFactory的函数，参数自己闭包，参数传递不放在这个函数的签名中
type InitFunc[Connection any] func(ctx context.Context) (*StorageLockFactory[Connection], error)

// Bean 存储在Bean工厂中的Bean，用于对同一个key对应的StorageLockFactory做单例
type Bean[Connection any] struct {
	Factory *StorageLockFactory[Connection]
	Err     error
}

// StorageLockFactoryBeanFactory 用于存储管理StorageLockFactory的BeanFactory
type StorageLockFactoryBeanFactory[Key comparable, Connection any] struct {
	keyStorageLockMap     map[Key]*Bean[Connection]
	keyStorageLockMapLock *sync.RWMutex
}

func NewStorageLockFactoryBeanFactory[Key comparable, Connection any]() *StorageLockFactoryBeanFactory[Key, Connection] {
	return &StorageLockFactoryBeanFactory[Key, Connection]{
		keyStorageLockMap:     make(map[Key]*Bean[Connection]),
		keyStorageLockMapLock: &sync.RWMutex{},
	}
}

// GetOrInit 获取或者初始化StorageLockFactory
func (x *StorageLockFactoryBeanFactory[Key, Connection]) GetOrInit(ctx context.Context, key Key, initFunc InitFunc[Connection]) (*StorageLockFactory[Connection], error) {

	// 如果已经存在了，则返回之前的结果
	bean, b := x.GetBean(key)
	if !b {
		// 如果没有存在的话，则尝试初始化
		bean = x.InitBean(ctx, key, initFunc)
	}

	return bean.Factory, bean.Err
}

// InitBean 初始化bean。
//
// 漏洞 L 修复：原实现 Lock() 后立即 Unlock()，后续 map 读写完全无锁，
// 并发调用会触发 Go 运行时 fatal error: concurrent map read and map write（不可 recover，
// 整个进程崩溃）；且两 goroutine 都会执行 initFunc、覆盖式写 map，被覆盖的工厂永不 Shutdown，
// 连接池/Storage 资源泄漏。
//
// 修复为正确的双重检查锁（DCL）：先读锁查，命中直接返回；未命中则取写锁，再查一次（防止
// 等锁期间已被别的 goroutine 初始化），仍未命中才执行 initFunc 并写回。initFunc 在写锁内
// 执行——工厂创建是低频操作，牺牲少量并发性能换取正确性（避免资源泄漏与崩溃）。
func (x *StorageLockFactoryBeanFactory[Key, Connection]) InitBean(ctx context.Context, key Key, initFunc InitFunc[Connection]) *Bean[Connection] {
	// 第一次检查：读锁
	x.keyStorageLockMapLock.RLock()
	bean, exists := x.keyStorageLockMap[key]
	x.keyStorageLockMapLock.RUnlock()
	if exists {
		return bean
	}

	// 未命中：取写锁做双重检查
	x.keyStorageLockMapLock.Lock()
	defer x.keyStorageLockMapLock.Unlock()
	// 双重检查：等锁期间可能已被别的 goroutine 初始化
	if bean, exists := x.keyStorageLockMap[key]; exists {
		return bean
	}

	factory, err := initFunc(ctx)
	bean = &Bean[Connection]{
		Factory: factory,
		Err:     err,
	}
	x.keyStorageLockMap[key] = bean

	return bean
}

// GetBean 加读锁尝试根据key读取bean
func (x *StorageLockFactoryBeanFactory[Key, Connection]) GetBean(key Key) (*Bean[Connection], bool) {
	x.keyStorageLockMapLock.RLock()
	defer x.keyStorageLockMapLock.RUnlock()

	bean, exists := x.keyStorageLockMap[key]
	return bean, exists
}

// Shutdown 关闭指定 key 的工厂。
//
// 漏洞5 修复：关闭后从 map 移除该 bean，否则 GetOrInit 仍会命中并返回"已死"工厂，
// 调用方 Lock 时走已 Close 的 Storage/ConnectionManager，且 map 永不缩容（资源泄漏）。
func (x *StorageLockFactoryBeanFactory[Key, Connection]) Shutdown(ctx context.Context, key Key) error {
	bean, b := x.GetBean(key)
	if !b {
		return fmt.Errorf("not found")
	}
	if bean.Err != nil {
		// 初始化失败的 bean 也从 map 移除，让后续 GetOrInit 可重试初始化
		x.keyStorageLockMapLock.Lock()
		delete(x.keyStorageLockMap, key)
		x.keyStorageLockMapLock.Unlock()
		return bean.Err
	}
	err := bean.Factory.Shutdown(ctx)
	// 无论 Shutdown 成功与否都移除：成功则避免返回已死工厂；失败则 bean 状态已不可靠，移除让下次重试
	x.keyStorageLockMapLock.Lock()
	delete(x.keyStorageLockMap, key)
	x.keyStorageLockMapLock.Unlock()
	return err
}

// ShutdownAll 关闭所有工厂。
//
// 漏洞 L 修复：原实现 Lock() 后立即 Unlock()，遍历 map 无锁，与并发的 InitBean/Remove
// 触发 concurrent map read and map write 致命崩溃。
// 修复：持写锁快照出所有 bean（仅拷贝指针，耗时极短），释放锁后再逐个 Shutdown——
// 避免持锁期间 Shutdown 耗时（关连接池）阻塞其它操作，也避免 Shutdown 内部若回调
// BeanFactory 造成死锁。
//
// 漏洞5 修复：Shutdown 完成后持写锁清空 map，避免 GetOrInit 命中"已死"工厂与 map 永不缩容。
func (x *StorageLockFactoryBeanFactory[Key, Connection]) ShutdownAll(ctx context.Context) map[Key]error {
	x.keyStorageLockMapLock.Lock()
	beans := make([]struct {
		key  Key
		bean *Bean[Connection]
	}, 0, len(x.keyStorageLockMap))
	for key, bean := range x.keyStorageLockMap {
		beans = append(beans, struct {
			key  Key
			bean *Bean[Connection]
		}{key: key, bean: bean})
	}
	x.keyStorageLockMapLock.Unlock()

	errorMap := make(map[Key]error)
	for _, b := range beans {
		if b.bean.Factory == nil {
			errorMap[b.key] = b.bean.Err
			continue
		}
		errorMap[b.key] = b.bean.Factory.Shutdown(ctx)
	}
	// 漏洞5：清空 map，已关闭的工厂不再被 GetOrInit 命中
	x.keyStorageLockMapLock.Lock()
	x.keyStorageLockMap = make(map[Key]*Bean[Connection])
	x.keyStorageLockMapLock.Unlock()
	return errorMap
}

// Remove 从BeanFactory中删除给定key的实例
func (x *StorageLockFactoryBeanFactory[Key, Connection]) Remove(key Key) (*StorageLockFactory[Connection], bool) {
	x.keyStorageLockMapLock.Lock()
	defer x.keyStorageLockMapLock.Unlock()

	bean, exists := x.keyStorageLockMap[key]
	delete(x.keyStorageLockMap, key)
	return bean.Factory, exists
}

func (x *StorageLockFactoryBeanFactory[Key, Connection]) VisitBeanMap(visitFunc func(beanMap map[Key]*Bean[Connection])) {
	x.keyStorageLockMapLock.Lock()
	defer x.keyStorageLockMapLock.Unlock()

	visitFunc(x.keyStorageLockMap)
}
