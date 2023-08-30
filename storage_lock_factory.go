package storage_lock_factory

import (
	"context"
	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// StorageLockFactory 锁的工厂，锁底层是有一些资源需要管理的，这些资源可能会被多次重复使用和不使用时的资源销毁等，这个Factory就是用来管理这个的
type StorageLockFactory[Connection any] struct {
	Storage           storage.Storage
	ConnectionManager storage.ConnectionManager[Connection]
}

func NewStorageLockFactory[Connection any](storage storage.Storage, manager storage.ConnectionManager[Connection]) *StorageLockFactory[Connection] {
	return &StorageLockFactory[Connection]{
		Storage:           storage,
		ConnectionManager: manager,
	}
}

func (x *StorageLockFactory[Connection]) CreateLock(lockId string) (*storage_lock.StorageLock, error) {
	return storage_lock.NewStorageLock(x.Storage, lockId)
}

func (x *StorageLockFactory[Connection]) CreateLockWithOptions(options *storage_lock.StorageLockOptions) (*storage_lock.StorageLock, error) {
	return storage_lock.NewStorageLockWithOptions(x.Storage, options)
}

func (x *StorageLockFactory[Connection]) Shutdown(ctx context.Context) error {

	// 关闭Storage
	if err := x.Storage.Close(ctx); err != nil {
		return err
	}

	// 关闭连接管理器
	if err := x.ConnectionManager.Shutdown(ctx); err != nil {
		return err
	}

	return nil
}
