package storage_lock_factory

import (
	"context"
	"testing"
	"time"

	"github.com/golang-infrastructure/go-iterator"
	"github.com/storage-lock/go-storage"
	"github.com/storage-lock/go-storage-lock"
	"github.com/stretchr/testify/assert"
)

// mockReentrantStorage 实现最小 Storage 接口，用于可重入锁测试
type mockReentrantStorage struct {
	owner string
}

func (m *mockReentrantStorage) Capabilities() []storage.StorageCapability {
	return []storage.StorageCapability{storage.CapabilityCAS, storage.CapabilityReliableTime, storage.CapabilityAtomicDelete}
}

func (m *mockReentrantStorage) CreateWithVersion(ctx context.Context, lockId string, version storage.Version, lockInformation *storage.LockInformation) error {
	if m.owner != "" && m.owner != lockInformation.OwnerId {
		return storage_lock.ErrVersionMiss
	}
	m.owner = lockInformation.OwnerId
	return nil
}

func (m *mockReentrantStorage) UpdateWithVersion(ctx context.Context, lockId string, expectedVersion, newVersion storage.Version, lockInformation *storage.LockInformation) error {
	if m.owner != lockInformation.OwnerId {
		return storage_lock.ErrVersionMiss
	}
	return nil
}

func (m *mockReentrantStorage) DeleteWithVersion(ctx context.Context, lockId string, expectedVersion storage.Version, lockInformation *storage.LockInformation) error {
	if m.owner != lockInformation.OwnerId {
		return storage_lock.ErrVersionMiss
	}
	m.owner = ""
	return nil
}

func (m *mockReentrantStorage) Get(ctx context.Context, lockId string) (string, error) {
	if m.owner == "" {
		return "", storage_lock.ErrLockNotFound
	}
	return `{"lock_id":"` + lockId + `","owner_id":"` + m.owner + `","version":1,"lock_count":1,"lock_begin_time":"2024-01-01T00:00:00Z","lease_expire_time":"2024-01-01T05:00:00Z"}`, nil
}

func (m *mockReentrantStorage) GetTime(ctx context.Context) (time.Time, error) {
	return time.Now(), nil
}

func (m *mockReentrantStorage) List(ctx context.Context) (iterator.Iterator[*storage.LockInformation], error) {
	return nil, nil
}

func (m *mockReentrantStorage) Close(ctx context.Context) error {
	return nil
}

func (m *mockReentrantStorage) Init(ctx context.Context) error {
	return nil
}

func (m *mockReentrantStorage) GetName() string {
	return "mock-reentrant-storage"
}

func TestNewReentrantLock_NilStorage(t *testing.T) {
	lock, err := NewReentrantLock(nil)
	assert.Nil(t, lock)
	assert.Error(t, err)
}

func TestReentrantLock_Lock_EmptyLockId(t *testing.T) {
	lock, err := NewReentrantLock(&mockReentrantStorage{})
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "", "owner-1")
	assert.Error(t, err)
}

func TestReentrantLock_Lock_EmptyOwnerId(t *testing.T) {
	lock, err := NewReentrantLock(&mockReentrantStorage{})
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "")
	assert.Error(t, err)
}

func TestReentrantLock_Lock_ReentrantSameOwner(t *testing.T) {
	storage := &mockReentrantStorage{}
	lock, err := NewReentrantLock(storage)
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)

	err = lock.Lock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)
}

func TestReentrantLock_UnLock_ReentrantSameOwner(t *testing.T) {
	storage := &mockReentrantStorage{}
	lock, err := NewReentrantLock(storage)
	assert.NoError(t, err)

	_ = lock.Lock(context.Background(), "lock-1", "owner-1")
	_ = lock.Lock(context.Background(), "lock-1", "owner-1")
	_ = lock.Lock(context.Background(), "lock-1", "owner-1")

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)
	assert.Equal(t, "owner-1", storage.owner)

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)
	assert.Equal(t, "owner-1", storage.owner)

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
	assert.NoError(t, err)
	assert.Equal(t, "", storage.owner)
}

func TestReentrantLock_UnLock_NotHeld(t *testing.T) {
	lock, err := NewReentrantLock(&mockReentrantStorage{})
	assert.NoError(t, err)

	err = lock.UnLock(context.Background(), "lock-1", "owner-1")
	assert.Error(t, err)
}

func TestReentrantLock_UnLock_WrongOwner(t *testing.T) {
	storage := &mockReentrantStorage{}
	lock, err := NewReentrantLock(storage)
	assert.NoError(t, err)

	_ = lock.Lock(context.Background(), "lock-1", "owner-1")

	err = lock.UnLock(context.Background(), "lock-1", "owner-2")
	assert.Error(t, err)
}
