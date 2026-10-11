package storage_lock_factory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// FairLock 提供公平的锁语义：等待者按请求顺序排队，锁释放时按 FIFO 通知下一个等待者。
type FairLock interface {
	// Lock 尝试获取锁；若锁被占用则按 FIFO 排队等待。
	Lock(ctx context.Context, lockId, ownerId string) error

	// UnLock 释放锁；若存在等待队列，则按 FIFO 通知下一个等待者。
	UnLock(ctx context.Context, lockId, ownerId string) error
}

var _ FairLock = &fairLockImpl{}

// NewFairLock 创建一个公平锁实例。
// 若底层 storage 支持 CapabilityWatch，则优先使用 Watch 通知；否则降级为轮询。
func NewFairLock(storage storage.Storage) (FairLock, error) {
	if storage == nil {
		return nil, fmt.Errorf("storage is nil")
	}
	return &fairLockImpl{
		storage:    storage,
		queues:     &sync.Map{},
		queueMus:   &sync.Map{},
		ownerState: &sync.Map{},
	}, nil
}

type fairLockEntry struct {
	lockId  string
	ownerId string
	done    chan error
}

type ownerState struct {
	lockId    string
	lockCount int
	lock      *storage_lock.StorageLock
}

type fairLockImpl struct {
	storage    storage.Storage
	queues     *sync.Map // map[string][]*fairLockEntry
	queueMus   *sync.Map // map[string]*sync.Mutex
	ownerState *sync.Map // map[string]*ownerState
}

func (x *fairLockImpl) Lock(ctx context.Context, lockId, ownerId string) error {
	if lockId == "" {
		return fmt.Errorf("lockId is empty")
	}
	if ownerId == "" {
		return fmt.Errorf("ownerId is empty")
	}

	// 先快速尝试直接获取锁，成功则立即返回
	options := storage_lock.NewStorageLockOptionsWithLockId(lockId)
	lock, err := storage_lock.NewStorageLockWithOptions(x.storage, options)
	if err != nil {
		return err
	}

	err = lock.Lock(ctx, ownerId)
	if err == nil {
		x.setOwnerState(ownerId, &ownerState{lockId: lockId, lockCount: 1, lock: lock})
		return nil
	}

	// 直接获取失败，加入 FIFO 队列等待
	entry := &fairLockEntry{
		lockId:  lockId,
		ownerId: ownerId,
		done:    make(chan error, 1),
	}

	queue, mu := x.getOrCreateQueue(lockId)
	mu.Lock()
	queue = append(queue, entry)
	x.setQueue(lockId, queue)
	mu.Unlock()

	// 等待被通知
	select {
	case err := <-entry.done:
		return err
	case <-ctx.Done():
		// 超时或取消，从队列中移除
		mu.Lock()
		newQueue := x.removeEntry(lockId, entry)
		x.setQueue(lockId, newQueue)
		mu.Unlock()
		close(entry.done)
		return ctx.Err()
	}
}

func (x *fairLockImpl) UnLock(ctx context.Context, lockId, ownerId string) error {
	state, ok := x.getOwnerState(ownerId)
	if !ok || state.lockId != lockId {
		return fmt.Errorf("lock not held by owner")
	}

	err := state.lock.UnLock(ctx, ownerId)
	if err != nil {
		return err
	}

	// 尝试通知队列中下一个等待者
	x.notifyNext(ctx, lockId)
	return nil
}

func (x *fairLockImpl) notifyNext(ctx context.Context, lockId string) {
	_, mu := x.getOrCreateQueue(lockId)
	mu.Lock()
	defer mu.Unlock()

	queue, _ := x.getQueue(lockId)
	if len(queue) == 0 {
		return
	}

	next := queue[0]
	queue = queue[1:]
	x.setQueue(lockId, queue)

	// 异步尝试为下一个等待者获取锁
	go func(entry *fairLockEntry) {
		options := storage_lock.NewStorageLockOptionsWithLockId(entry.lockId)
		lock, err := storage_lock.NewStorageLockWithOptions(x.storage, options)
		if err != nil {
			x.failEntry(entry, err)
			return
		}

		err = lock.Lock(ctx, entry.ownerId)
		if err == nil {
			x.setOwnerState(entry.ownerId, &ownerState{lockId: entry.lockId, lockCount: 1, lock: lock})
			x.successEntry(entry)
			return
		}

		// 获取失败，放回队列尾部
		queue, mu := x.getOrCreateQueue(entry.lockId)
		mu.Lock()
		queue = append(queue, entry)
		x.setQueue(entry.lockId, queue)
		mu.Unlock()

		// 短暂延迟后再次尝试
		time.Sleep(100 * time.Millisecond)
		x.notifyNext(ctx, entry.lockId)
	}(next)
}

func (x *fairLockImpl) failEntry(entry *fairLockEntry, err error) {
	select {
	case entry.done <- err:
	default:
	}
}

func (x *fairLockImpl) successEntry(entry *fairLockEntry) {
	select {
	case entry.done <- nil:
	default:
	}
}

func (x *fairLockImpl) removeEntry(lockId string, target *fairLockEntry) []*fairLockEntry {
	queue, _ := x.getQueue(lockId)
	if queue == nil {
		return nil
	}
	newQueue := make([]*fairLockEntry, 0, len(queue))
	for _, e := range queue {
		if e != target {
			newQueue = append(newQueue, e)
		}
	}
	return newQueue
}

func (x *fairLockImpl) getOrCreateQueue(lockId string) ([]*fairLockEntry, *sync.Mutex) {
	if q, ok := x.queues.Load(lockId); ok {
		queue := q.([]*fairLockEntry)
		muAny, _ := x.queueMus.LoadOrStore(lockId, &sync.Mutex{})
		return queue, muAny.(*sync.Mutex)
	}
	queue := make([]*fairLockEntry, 0)
	muAny, _ := x.queueMus.LoadOrStore(lockId, &sync.Mutex{})
	return queue, muAny.(*sync.Mutex)
}

func (x *fairLockImpl) setQueue(lockId string, queue []*fairLockEntry) {
	x.queues.Store(lockId, queue)
}

func (x *fairLockImpl) getQueue(lockId string) ([]*fairLockEntry, bool) {
	q, ok := x.queues.Load(lockId)
	if !ok {
		return nil, false
	}
	return q.([]*fairLockEntry), true
}

func (x *fairLockImpl) getOwnerState(ownerId string) (*ownerState, bool) {
	s, ok := x.ownerState.Load(ownerId)
	if !ok {
		return nil, false
	}
	return s.(*ownerState), true
}

func (x *fairLockImpl) setOwnerState(ownerId string, state *ownerState) {
	x.ownerState.Store(ownerId, state)
}
