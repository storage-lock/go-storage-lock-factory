package storage_lock_factory

import (
	"context"
	"fmt"
	"sync"

	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// ReentrantLock 提供可重入锁语义：同一 ownerId 对同一 lockId 可多次 Lock，
// 内部通过 LockCount 计数；仅当最后一次 UnLock 时才真正释放底层锁。
type ReentrantLock interface {
	Lock(ctx context.Context, lockId, ownerId string) error
	UnLock(ctx context.Context, lockId, ownerId string) error
}

var _ ReentrantLock = &reentrantLockImpl{}

// NewReentrantLock 创建一个可重入锁实例。
func NewReentrantLock(storage storage.Storage) (ReentrantLock, error) {
	if storage == nil {
		return nil, fmt.Errorf("storage is nil")
	}
	return &reentrantLockImpl{
		storage:    storage,
		ownerState: &sync.Map{},
	}, nil
}

type reentrantOwnerState struct {
	lockId    string
	lockCount int
	lock      *storage_lock.StorageLock
}

type reentrantLockImpl struct {
	storage    storage.Storage
	ownerState *sync.Map // map[string]*reentrantOwnerState keyed by ownerId
}

func (x *reentrantLockImpl) Lock(ctx context.Context, lockId, ownerId string) error {
	if lockId == "" {
		return fmt.Errorf("lockId is empty")
	}
	if ownerId == "" {
		return fmt.Errorf("ownerId is empty")
	}

	state, ok := x.getOwnerState(ownerId)
	if ok && state.lockId == lockId {
		state.lockCount++
		x.setOwnerState(ownerId, state)
		return nil
	}

	options := storage_lock.NewStorageLockOptionsWithLockId(lockId)
	lock, err := storage_lock.NewStorageLockWithOptions(x.storage, options)
	if err != nil {
		return err
	}

	err = lock.Lock(ctx, ownerId)
	if err != nil {
		return err
	}

	x.setOwnerState(ownerId, &reentrantOwnerState{
		lockId:    lockId,
		lockCount: 1,
		lock:      lock,
	})
	return nil
}

func (x *reentrantLockImpl) UnLock(ctx context.Context, lockId, ownerId string) error {
	state, ok := x.getOwnerState(ownerId)
	if !ok || state.lockId != lockId {
		return fmt.Errorf("lock not held by owner")
	}

	state.lockCount--
	if state.lockCount > 0 {
		x.setOwnerState(ownerId, state)
		return nil
	}

	err := state.lock.UnLock(ctx, ownerId)
	if err != nil {
		return err
	}

	x.ownerState.Delete(ownerId)
	return nil
}

func (x *reentrantLockImpl) getOwnerState(ownerId string) (*reentrantOwnerState, bool) {
	s, ok := x.ownerState.Load(ownerId)
	if !ok {
		return nil, false
	}
	return s.(*reentrantOwnerState), true
}

func (x *reentrantLockImpl) setOwnerState(ownerId string, state *reentrantOwnerState) {
	x.ownerState.Store(ownerId, state)
}
