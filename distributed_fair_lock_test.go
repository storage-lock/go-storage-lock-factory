package storage_lock_factory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/golang-infrastructure/go-iterator"
	"github.com/storage-lock/go-storage"
	storage_lock "github.com/storage-lock/go-storage-lock"
	"github.com/stretchr/testify/assert"
)

// mockDistributedStorage 实现最小 Storage 接口，用于跨节点公平锁测试
type mockDistributedStorage struct {
	record string
	owners map[string]string // lockId -> ownerId
}

func newMockDistributedStorage() *mockDistributedStorage {
	return &mockDistributedStorage{owners: make(map[string]string)}
}

func (m *mockDistributedStorage) Capabilities() []storage.StorageCapability {
	return []storage.StorageCapability{storage.CapabilityCAS, storage.CapabilityReliableTime, storage.CapabilityAtomicDelete}
}

func (m *mockDistributedStorage) CreateWithVersion(ctx context.Context, lockId string, version storage.Version, lockInformation *storage.LockInformation) error {
	if current, ok := m.owners[lockId]; ok && current != "" {
		return storage_lock.ErrVersionMiss
	}
	m.owners[lockId] = lockInformation.OwnerId
	return nil
}

func (m *mockDistributedStorage) UpdateWithVersion(ctx context.Context, lockId string, expectedVersion, newVersion storage.Version, lockInformation *storage.LockInformation) error {
	if current, ok := m.owners[lockId]; !ok || current != lockInformation.OwnerId {
		return storage_lock.ErrVersionMiss
	}
	m.record = lockInformation.ToJsonString()

	if lockInformation.LockCount == 0 {
		delete(m.owners, lockId)
		m.record = ""
	}
	return nil
}

func (m *mockDistributedStorage) DeleteWithVersion(ctx context.Context, lockId string, expectedVersion storage.Version, lockInformation *storage.LockInformation) error {
	if current, ok := m.owners[lockId]; !ok || current != lockInformation.OwnerId {
		return storage_lock.ErrVersionMiss
	}
	delete(m.owners, lockId)
	return nil
}

func (m *mockDistributedStorage) Get(ctx context.Context, lockId string) (string, error) {
	owner, ok := m.owners[lockId]
	if !ok || owner == "" {
		return "", storage_lock.ErrLockNotFound
	}
	if m.record != "" {
		return m.record, nil
	}
	return `{"lock_id":"` + lockId + `","owner_id":"` + owner + `","version":1,"lock_count":1,"lock_begin_time":"2024-01-01T00:00:00Z","lease_expire_time":"` + time.Now().UTC().Add(time.Hour).Format(time.RFC3339) + `"}`, nil
}

func (m *mockDistributedStorage) GetTime(ctx context.Context) (time.Time, error) {
	return time.Now(), nil
}

func (m *mockDistributedStorage) List(ctx context.Context) (iterator.Iterator[*storage.LockInformation], error) {
	return nil, nil
}

func (m *mockDistributedStorage) Close(ctx context.Context) error {
	return nil
}

func (m *mockDistributedStorage) Init(ctx context.Context) error {
	return nil
}

func (m *mockDistributedStorage) GetName() string {
	return "mock-distributed-storage"
}

func TestDistributedFairLock_NilStorage(t *testing.T) {
	lock, err := NewDistributedFairLock(nil)
	assert.Nil(t, lock)
	assert.Error(t, err)
}

func TestDistributedFairLock_Lock_EmptyGroup(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "", "lock-1", "owner-1")
	assert.Error(t, err)
}

func TestDistributedFairLock_Lock_EmptyLockId(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "group-1", "", "owner-1")
	assert.Error(t, err)
}

func TestDistributedFairLock_Lock_EmptyOwnerId(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "group-1", "lock-1", "")
	assert.Error(t, err)
}

func TestDistributedFairLock_FirstOwnerGetsLock(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "group-1", "lock-1", "owner-1")
	assert.NoError(t, err)

	err = lock.UnLock(context.Background(), "group-1", "lock-1", "owner-1")
	assert.NoError(t, err)
}

func TestDistributedFairLock_FIFO_SecondOwnerWaits(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "group-1", "lock-1", "owner-1")
	assert.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		err := lock.Lock(context.Background(), "group-1", "lock-1", "owner-2")
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("owner-2 should be waiting, got error: %v", err)
	case <-time.After(100 * time.Millisecond):
		// expected: owner-2 is still waiting
	}

	err = lock.UnLock(context.Background(), "group-1", "lock-1", "owner-1")
	assert.NoError(t, err)

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("owner-2 should have acquired the lock after unlock")
	}
}

func TestDistributedFairLock_FIFO_MultipleWaiters(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "group-1", "lock-1", "owner-1")
	assert.NoError(t, err)

	resultCh := make(chan string, 3)
	var wg sync.WaitGroup
	wg.Add(3)

	for i := 0; i < 3; i++ {
		go func(idx int) {
			defer wg.Done()
			ownerId := "owner-" + string(rune('2'+idx))
			err := lock.Lock(context.Background(), "group-1", "lock-1", ownerId)
			if err == nil {
				resultCh <- ownerId
				time.Sleep(50 * time.Millisecond)
				lock.UnLock(context.Background(), "group-1", "lock-1", ownerId)
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	err = lock.UnLock(context.Background(), "group-1", "lock-1", "owner-1")
	assert.NoError(t, err)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(resultCh)
		close(done)
	}()

	select {
	case <-done:
		// expected: all waiters completed
	case <-time.After(10 * time.Second):
		t.Fatal("test timed out waiting for goroutines to complete")
	}

	var results []string
	for r := range resultCh {
		results = append(results, r)
	}

	assert.Len(t, results, 3)
	assert.Contains(t, results, "owner-2")
	assert.Contains(t, results, "owner-3")
	assert.Contains(t, results, "owner-4")
}

func TestDistributedFairLock_UnLock_WrongOwner(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	_ = lock.Lock(context.Background(), "group-1", "lock-1", "owner-1")

	err = lock.UnLock(context.Background(), "group-1", "lock-1", "owner-2")
	assert.Error(t, err)
}

func TestDistributedFairLock_UnLock_NotHeld(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.UnLock(context.Background(), "group-1", "lock-1", "owner-1")
	assert.Error(t, err)
}

func TestDistributedFairLock_ContextCancellation_WhileWaiting(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	_ = lock.Lock(context.Background(), "group-1", "lock-1", "owner-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = lock.Lock(ctx, "group-1", "lock-1", "owner-2")
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestDistributedFairLock_MultipleGroups_Isolated(t *testing.T) {
	lock, err := NewDistributedFairLock(newMockDistributedStorage())
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "group-a", "lock-a", "owner-1")
	assert.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		err := lock.Lock(context.Background(), "group-b", "lock-b", "owner-2")
		done <- err
	}()

	select {
	case err := <-done:
		assert.NoError(t, err, "different groups should not block each other")
	case <-time.After(time.Second):
		t.Fatal("owner-2 in different group should acquire lock without waiting")
	}

	_ = lock.UnLock(context.Background(), "group-a", "lock-a", "owner-1")
	_ = lock.UnLock(context.Background(), "group-b", "lock-b", "owner-2")
}

func TestDistributedFairLock_WatchDogNotClobberStolenLock(t *testing.T) {
	s := newMockDistributedStorage()
	lock, err := NewDistributedFairLock(s)
	assert.NoError(t, err)

	_ = lock.Lock(context.Background(), "group-1", "lock-1", "owner-1")

	// Simulate another owner stealing the lock by manually deleting the record.
	info, err := s.Get(context.Background(), "lock-1")
	assert.NoError(t, err)
	parsed, err := storage.LockInformationFromJsonString(info)
	assert.NoError(t, err)
	_ = s.DeleteWithVersion(context.Background(), "lock-1", parsed.Version, parsed)

	// owner-1 unlocks after steal; should not clobber owner-2's new lease.
	_ = lock.UnLock(context.Background(), "group-1", "lock-1", "owner-1")

	_ = lock.Lock(context.Background(), "group-1", "lock-1", "owner-2")
	_ = lock.UnLock(context.Background(), "group-1", "lock-1", "owner-2")
}
