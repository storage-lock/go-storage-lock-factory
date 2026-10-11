package storage_lock_factory

import (
	"context"
	"testing"
	"time"

	"github.com/golang-infrastructure/go-iterator"
	"github.com/storage-lock/go-storage"
	"github.com/stretchr/testify/assert"
)

// mockWatcherStorage 模拟支持 Watch 能力的存储
type mockWatcherStorage struct {
	watchable bool
	events    []*storage.LockInformation
}

func newMockWatcherStorage(events ...*storage.LockInformation) *mockWatcherStorage {
	return &mockWatcherStorage{watchable: true, events: events}
}

func (m *mockWatcherStorage) Capabilities() []storage.StorageCapability {
	return []storage.StorageCapability{storage.CapabilityCAS, storage.CapabilityReliableTime}
}

func (m *mockWatcherStorage) CreateWithVersion(ctx context.Context, lockId string, version storage.Version, lockInformation *storage.LockInformation) error {
	return nil
}

func (m *mockWatcherStorage) UpdateWithVersion(ctx context.Context, lockId string, expectedVersion, newVersion storage.Version, lockInformation *storage.LockInformation) error {
	return nil
}

func (m *mockWatcherStorage) DeleteWithVersion(ctx context.Context, lockId string, expectedVersion storage.Version, lockInformation *storage.LockInformation) error {
	return nil
}

func (m *mockWatcherStorage) Get(ctx context.Context, lockId string) (string, error) {
	return "", nil
}

func (m *mockWatcherStorage) GetTime(ctx context.Context) (time.Time, error) {
	return time.Now(), nil
}

func (m *mockWatcherStorage) List(ctx context.Context) (iterator.Iterator[*storage.LockInformation], error) {
	return nil, nil
}

func (m *mockWatcherStorage) Close(ctx context.Context) error {
	return nil
}

func (m *mockWatcherStorage) Init(ctx context.Context) error {
	return nil
}

func (m *mockWatcherStorage) GetName() string {
	return "mock-watcher-storage"
}

func (m *mockWatcherStorage) Watch(ctx context.Context, lockId string) (<-chan *storage.LockInformation, error) {
	if !m.watchable {
		return nil, assert.AnError
	}
	ch := make(chan *storage.LockInformation, len(m.events))
	go func() {
		defer close(ch)
		for _, info := range m.events {
			select {
			case <-ctx.Done():
				return
			case ch <- info:
			}
		}
	}()
	return ch, nil
}

// mockNonWatchableStorage 模拟不支持 Watch 的存储
type mockNonWatchableStorage struct{}

func (m *mockNonWatchableStorage) Capabilities() []storage.StorageCapability {
	return []storage.StorageCapability{storage.CapabilityCAS, storage.CapabilityReliableTime}
}

func (m *mockNonWatchableStorage) CreateWithVersion(ctx context.Context, lockId string, version storage.Version, lockInformation *storage.LockInformation) error {
	return nil
}

func (m *mockNonWatchableStorage) UpdateWithVersion(ctx context.Context, lockId string, expectedVersion, newVersion storage.Version, lockInformation *storage.LockInformation) error {
	return nil
}

func (m *mockNonWatchableStorage) DeleteWithVersion(ctx context.Context, lockId string, expectedVersion storage.Version, lockInformation *storage.LockInformation) error {
	return nil
}

func (m *mockNonWatchableStorage) Get(ctx context.Context, lockId string) (string, error) {
	return `{"lock_id":"lock-1","owner_id":"owner-1","version":1,"lock_count":1,"lock_begin_time":"2024-01-01T00:00:00Z","lease_expire_time":"2024-01-01T05:00:00Z"}`, nil
}

func (m *mockNonWatchableStorage) GetTime(ctx context.Context) (time.Time, error) {
	return time.Now(), nil
}

func (m *mockNonWatchableStorage) List(ctx context.Context) (iterator.Iterator[*storage.LockInformation], error) {
	return nil, nil
}

func (m *mockNonWatchableStorage) Close(ctx context.Context) error {
	return nil
}

func (m *mockNonWatchableStorage) Init(ctx context.Context) error {
	return nil
}

func (m *mockNonWatchableStorage) GetName() string {
	return "mock-non-watchable-storage"
}

func TestNewLockWatcher_NilStorage(t *testing.T) {
	watcher, err := NewLockWatcher(nil)
	assert.Nil(t, watcher)
	assert.Error(t, err)
}

func TestNewLockWatcher_NonWatchableStorage(t *testing.T) {
	storage := &mockNonWatchableStorage{}
	watcher, err := NewLockWatcher(storage)
	assert.NoError(t, err)
	assert.NotNil(t, watcher)
	assert.IsType(t, &pollingLockWatcher{}, watcher)
}

func TestNewLockWatcher_WatchableStorage(t *testing.T) {
	events := []*storage.LockInformation{
		{LockId: "lock-1", OwnerId: "owner-1", Version: 1, LockCount: 1},
	}
	storage := newMockWatcherStorage(events...)
	watcher, err := NewLockWatcher(storage)
	assert.NoError(t, err)
	assert.NotNil(t, watcher)
}

func TestPollingLockWatcher_Watch_EmptyLockId(t *testing.T) {
	storage := &mockNonWatchableStorage{}
	watcher, err := NewLockWatcher(storage)
	assert.NoError(t, err)

	ch, err := watcher.Watch(context.Background(), "")
	assert.Error(t, err)
	assert.Nil(t, ch)
}

func TestPollingLockWatcher_Watch_ContextCancelled(t *testing.T) {
	storage := &mockNonWatchableStorage{}
	watcher, err := NewLockWatcher(storage)
	assert.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := watcher.Watch(ctx, "lock-1")
	assert.NoError(t, err)
	assert.NotNil(t, ch)

	// channel 应该因为 ctx 取消而关闭
	for range ch {
		t.Fatal("should not receive any events")
	}
}

func TestPollingLockWatcher_Close(t *testing.T) {
	storage := &mockNonWatchableStorage{}
	watcher, err := NewLockWatcher(storage)
	assert.NoError(t, err)

	err = watcher.Close()
	assert.NoError(t, err)

	// 再次关闭应该不报错
	err = watcher.Close()
	assert.NoError(t, err)
}
