package storage_lock_factory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/golang-infrastructure/go-iterator"
	"github.com/storage-lock/go-storage"
	"github.com/storage-lock/go-storage-lock"
	"github.com/stretchr/testify/assert"
)

// mockFairLockStorage 实现最小 Storage 接口，用于公平锁测试
type mockFairLockStorage struct {
	mu        sync.Mutex
	owner     string
	waitCount int
}

func (m *mockFairLockStorage) Capabilities() []storage.StorageCapability {
	return []storage.StorageCapability{storage.CapabilityCAS, storage.CapabilityReliableTime, storage.CapabilityAtomicDelete}
}

func (m *mockFairLockStorage) CreateWithVersion(ctx context.Context, lockId string, version storage.Version, lockInformation *storage.LockInformation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != "" {
		return storage_lock.ErrVersionMiss
	}
	m.owner = lockInformation.OwnerId
	return nil
}

func (m *mockFairLockStorage) UpdateWithVersion(ctx context.Context, lockId string, expectedVersion, newVersion storage.Version, lockInformation *storage.LockInformation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != lockInformation.OwnerId {
		return storage_lock.ErrVersionMiss
	}
	return nil
}

func (m *mockFairLockStorage) DeleteWithVersion(ctx context.Context, lockId string, expectedVersion storage.Version, lockInformation *storage.LockInformation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner != lockInformation.OwnerId {
		return storage_lock.ErrVersionMiss
	}
	m.owner = ""
	return nil
}

func (m *mockFairLockStorage) Get(ctx context.Context, lockId string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.owner == "" {
		return "", storage_lock.ErrLockNotFound
	}
	return `{"lock_id":"` + lockId + `","owner_id":"` + m.owner + `","version":1,"lock_count":1,"lock_begin_time":"2024-01-01T00:00:00Z","lease_expire_time":"2024-01-01T05:00:00Z"}`, nil
}

func (m *mockFairLockStorage) GetTime(ctx context.Context) (time.Time, error) {
	return time.Now(), nil
}

func (m *mockFairLockStorage) List(ctx context.Context) (iterator.Iterator[*storage.LockInformation], error) {
	return nil, nil
}

func (m *mockFairLockStorage) Close(ctx context.Context) error {
	return nil
}

func (m *mockFairLockStorage) Init(ctx context.Context) error {
	return nil
}

func (m *mockFairLockStorage) GetName() string {
	return "mock-fair-lock-storage"
}

func TestNewFairLock_NilStorage(t *testing.T) {
	lock, err := NewFairLock(nil)
	assert.Nil(t, lock)
	assert.Error(t, err)
}

func TestFairLock_Lock_EmptyLockId(t *testing.T) {
	lock, err := NewFairLock(&mockFairLockStorage{})
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "", "owner-1")
	assert.Error(t, err)
}

func TestFairLock_Lock_EmptyOwnerId(t *testing.T) {
	lock, err := NewFairLock(&mockFairLockStorage{})
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "")
	assert.Error(t, err)
}

func TestFairLock_Lock_FirstOwnerGetsLock(t *testing.T) {
	storage := &mockFairLockStorage{}
	lock, err := NewFairLock(storage)
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)
}

func TestFairLock_UnLock_ReleasesLock(t *testing.T) {
	storage := &mockFairLockStorage{}
	lock, err := NewFairLock(storage)
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)
	assert.Equal(t, "", storage.owner)
}

func TestFairLock_FIFO_SecondOwnerWaits(t *testing.T) {
	storage := &mockFairLockStorage{}
	lock, err := NewFairLock(storage)
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		err := lock.Lock(context.Background(), "lock-1", "owner-2")
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("owner-2 should be waiting, got error: %v", err)
	case <-time.After(100 * time.Millisecond):
		// expected: owner-2 is still waiting
	}

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("owner-2 should have acquired the lock after unlock")
	}
}

func TestFairLock_FIFO_MultipleWaiters(t *testing.T) {
	storage := &mockFairLockStorage{}
	lock, err := NewFairLock(storage)
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)

	resultCh := make(chan string, 3)
	var wg sync.WaitGroup
	wg.Add(3)

	for i := 0; i < 3; i++ {
		go func(idx int) {
			defer wg.Done()
			ownerId := "owner-" + string(rune('2'+idx))
			err := lock.Lock(context.Background(), "lock-1", ownerId)
			if err == nil {
				resultCh <- ownerId
				time.Sleep(50 * time.Millisecond)
				lock.UnLock(context.Background(), "lock-1", ownerId)
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
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

func TestFairLock_UnLock_WrongOwner(t *testing.T) {
	storage := &mockFairLockStorage{}
	lock, err := NewFairLock(storage)
	assert.NoError(t, err)

	_ = lock.Lock(context.Background(), "lock-1", "owner-1")

	err = lock.UnLock(context.Background(), "lock-1", "owner-2")
	assert.Error(t, err)
}

func TestFairLock_UnLock_NotHeld(t *testing.T) {
	lock, err := NewFairLock(&mockFairLockStorage{})
	assert.NoError(t, err)

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
	assert.Error(t, err)
}

func TestFairLock_ContextCancellation_WhileWaiting(t *testing.T) {
	storage := &mockFairLockStorage{}
	lock, err := NewFairLock(storage)
	assert.NoError(t, err)

	_ = lock.Lock(context.Background(), "lock-1", "owner-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = lock.Lock(ctx, "lock-1", "owner-2")
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
