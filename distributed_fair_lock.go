package storage_lock_factory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
)

// DistributedFairLock 提供跨多个公平锁实例的协调能力。
// 它本身不直接创建锁，而是把多个 FairLock 实例纳入同一个逻辑锁组，
// 在“某个实例持有底层锁”时广播给同组其他等待者，减少无效抢占。
type DistributedFairLock interface {
	// Lock 按组内 FIFO 协调后获取锁。
	Lock(ctx context.Context, group, lockId, ownerId string) error
	// UnLock 释放锁并通知同组下一个等待者。
	UnLock(ctx context.Context, group, lockId, ownerId string) error
}

var _ DistributedFairLock = &distributedFairLockImpl{}

// NewDistributedFairLock 创建一个跨实例公平锁协调器。
func NewDistributedFairLock(storage storage.Storage) (DistributedFairLock, error) {
	if storage == nil {
		return nil, fmt.Errorf("storage is nil")
	}
	return &distributedFairLockImpl{
		storage:    storage,
		groups:     &sync.Map{},
		groupMus:   &sync.Map{},
		ownerState: &sync.Map{},
	}, nil
}

type distributedFairLockEntry struct {
	lockId  string
	ownerId string
	done    chan error
}

type distributedFairLockImpl struct {
	storage    storage.Storage
	groups     *sync.Map // map[string][]*distributedFairLockEntry
	groupMus   *sync.Map // map[string]*sync.Mutex
	ownerState *sync.Map // map[string]*ownerState
}

func (x *distributedFairLockImpl) Lock(ctx context.Context, group, lockId, ownerId string) error {
	if group == "" {
		return fmt.Errorf("group is empty")
	}
	if lockId == "" {
		return fmt.Errorf("lockId is empty")
	}
	if ownerId == emptyOwnerId {
		return fmt.Errorf("ownerId is empty")
	}

	// 重入：同一 owner 对同一 lockId 已持有，直接递增计数
	if state, ok := x.getOwnerState(ownerId); ok && state.lockId == lockId {
		state.lockCount++
		x.setOwnerState(ownerId, state)
		return nil
	}

	// 先快速尝试直接获取锁，成功则立即返回，不进队
	options := storage_lock.NewStorageLockOptionsWithLockId(lockId)
	directLock, err := storage_lock.NewStorageLockWithOptions(x.storage, options)
	if err != nil {
		return err
	}
	err = directLock.Lock(ctx, ownerId)
	if err == nil {
		x.setOwnerState(ownerId, &ownerState{lockId: lockId, lockCount: 1, lock: directLock})
		return nil
	}

	// 直接获取失败，加入 FIFO 队列等待
	entry := &distributedFairLockEntry{
		lockId:  lockId,
		ownerId: ownerId,
		done:    make(chan error, 1),
	}

	queue, mu := x.getOrCreateGroup(group)
	mu.Lock()
	queue = append(queue, entry)
	x.setGroup(group, queue)
	mu.Unlock()

	select {
	case err := <-entry.done:
		return err
	case <-ctx.Done():
		mu.Lock()
		newQueue := x.removeEntry(group, entry)
		x.setGroup(group, newQueue)
		mu.Unlock()
		close(entry.done)
		return ctx.Err()
	}
}

func (x *distributedFairLockImpl) UnLock(ctx context.Context, group, lockId, ownerId string) error {
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
	x.notifyNext(ctx, group)
	return nil
}

func (x *distributedFairLockImpl) notifyNext(ctx context.Context, group string) {
	_, mu := x.getOrCreateGroup(group)
	mu.Lock()
	defer mu.Unlock()

	queue, ok := x.getGroup(group)
	if !ok || len(queue) == 0 {
		return
	}

	next := queue[0]
	queue = queue[1:]
	x.setGroup(group, queue)

	go func(entry *distributedFairLockEntry) {
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

		queue, mu := x.getOrCreateGroup(group)
		mu.Lock()
		queue = append(queue, entry)
		x.setGroup(group, queue)
		mu.Unlock()

		time.Sleep(100 * time.Millisecond)
		x.notifyNext(ctx, group)
	}(next)
}

func (x *distributedFairLockImpl) failEntry(entry *distributedFairLockEntry, err error) {
	select {
	case entry.done <- err:
	default:
	}
}

func (x *distributedFairLockImpl) successEntry(entry *distributedFairLockEntry) {
	select {
	case entry.done <- nil:
	default:
	}
}

func (x *distributedFairLockImpl) removeEntry(group string, target *distributedFairLockEntry) []*distributedFairLockEntry {
	queue, ok := x.getGroup(group)
	if !ok || queue == nil {
		return nil
	}
	newQueue := make([]*distributedFairLockEntry, 0, len(queue))
	for _, e := range queue {
		if e != target {
			newQueue = append(newQueue, e)
		}
	}
	return newQueue
}

func (x *distributedFairLockImpl) getOrCreateGroup(group string) ([]*distributedFairLockEntry, *sync.Mutex) {
	if q, ok := x.groups.Load(group); ok {
		queue := q.([]*distributedFairLockEntry)
		muAny, _ := x.groupMus.LoadOrStore(group, &sync.Mutex{})
		return queue, muAny.(*sync.Mutex)
	}
	queue := make([]*distributedFairLockEntry, 0)
	muAny, _ := x.groupMus.LoadOrStore(group, &sync.Mutex{})
	return queue, muAny.(*sync.Mutex)
}

func (x *distributedFairLockImpl) setGroup(group string, queue []*distributedFairLockEntry) {
	x.groups.Store(group, queue)
}

func (x *distributedFairLockImpl) getGroup(group string) ([]*distributedFairLockEntry, bool) {
	q, ok := x.groups.Load(group)
	if !ok {
		return nil, false
	}
	return q.([]*distributedFairLockEntry), true
}

func (x *distributedFairLockImpl) getOwnerState(ownerId string) (*ownerState, bool) {
	s, ok := x.ownerState.Load(ownerId)
	if !ok {
		return nil, false
	}
	return s.(*ownerState), true
}

func (x *distributedFairLockImpl) setOwnerState(ownerId string, state *ownerState) {
	x.ownerState.Store(ownerId, state)
}

const emptyOwnerId = ""
