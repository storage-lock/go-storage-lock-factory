package storage_lock_factory

import (
	"context"
	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

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

func (x *StorageLockFactory[Connection]) CreateLockWithOptions(lockId string, options *storage_lock.StorageLockOptions) (*storage_lock.StorageLock, error) {
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
