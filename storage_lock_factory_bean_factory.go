package storage_lock_factory

import (
	"context"
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

// InitBean 初始化bean
func (x *StorageLockFactoryBeanFactory[Key, Connection]) InitBean(ctx context.Context, key Key, initFunc InitFunc[Connection]) *Bean[Connection] {
	x.keyStorageLockMapLock.Lock()
	x.keyStorageLockMapLock.Unlock()

	// D-C-L
	bean, exists := x.keyStorageLockMap[key]
	if exists {
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
