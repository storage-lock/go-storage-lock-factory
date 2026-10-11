package storage_lock_factory

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/storage-lock/go-storage"
)

// lockWatchableMethodType is the reflect.Type for context.Context argument in Watch signature.
// This lets us detect the optional interface without a direct type assertion that currently fails to compile.
var lockWatchableMethodType = reflect.TypeOf((*context.Context)(nil)).Elem()

// hasWatchMethod reports whether v implements a Watch(ctx context.Context, lockId string) (chan *storage.LockInformation, error) method.
func hasWatchMethod(v interface{}) bool {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr && val.IsNil() {
		return false
	}
	method, ok := val.Type().MethodByName("Watch")
	if !ok {
		return false
	}
	if method.Type.NumIn() != 3 {
		return false
	}
	if !method.Type.In(1).AssignableTo(lockWatchableMethodType) {
		return false
	}
	return true
}

// LockWatcher 是锁状态变化监听器的抽象，统一封装底层存储的 Watch/Pub-Sub/轮询能力。
type LockWatcher interface {
	// Watch 监听指定锁的状态变化。
	// 每次调用返回一个新的 channel；调用方负责读取或超时退出。
	Watch(ctx context.Context, lockId string) (<-chan *storage.LockInformation, error)

	// Close 关闭监听器并释放底层资源。
	Close() error
}

var _ LockWatcher = &pollingLockWatcher{}

// NewLockWatcher 根据存储能力自动选择 LockWatcher 实现。
//   - 若 storage 实现 storage.LockWatchable 接口，优先使用原生 Watch；
//   - 否则降级为轮询 watcher。
func NewLockWatcher(storage storage.Storage) (LockWatcher, error) {
	if storage == nil {
		return nil, fmt.Errorf("storage is nil")
	}

	// 尝试通过反射检查是否具备 Watch 能力，避免直接类型断言。
	if hasWatchMethod(storage) {
		return &nativeLockWatcher{
			watchable: storage,
		}, nil
	}

	return NewPollingLockWatcher(storage, time.Second), nil
}

// nativeLockWatcher 直接包装实现了 LockWatchable 的存储。
type nativeLockWatcher struct {
	watchable storage.Storage
}

func (x *nativeLockWatcher) Watch(ctx context.Context, lockId string) (<-chan *storage.LockInformation, error) {
	v := reflect.ValueOf(x.watchable)
	method := v.MethodByName("Watch")
	if !method.IsValid() {
		return nil, fmt.Errorf("watch not supported")
	}

	args := []reflect.Value{
		reflect.ValueOf(ctx),
		reflect.ValueOf(lockId),
	}
	results := method.Call(args)

	if !results[1].IsNil() {
		return nil, results[1].Interface().(error)
	}
	return results[0].Interface().(<-chan *storage.LockInformation), nil
}

func (x *nativeLockWatcher) Close() error {
	return nil
}

// pollingLockWatcher 对不支持原生 Watch 的存储提供轮询降级。
type pollingLockWatcher struct {
	storage    storage.Storage
	pollPeriod time.Duration
	closeOnce  sync.Once
	closed     chan struct{}
}

func NewPollingLockWatcher(storage storage.Storage, pollPeriod time.Duration) *pollingLockWatcher {
	return &pollingLockWatcher{
		storage:    storage,
		pollPeriod: pollPeriod,
		closed:     make(chan struct{}),
	}
}

func (x *pollingLockWatcher) Watch(ctx context.Context, lockId string) (<-chan *storage.LockInformation, error) {
	if lockId == "" {
		return nil, fmt.Errorf("lockId is empty")
	}

	out := make(chan *storage.LockInformation, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(x.pollPeriod)
		defer ticker.Stop()

		var lastVersion storage.Version
		for {
			select {
			case <-ctx.Done():
				return
			case <-x.closed:
				return
			case <-ticker.C:
			}

			jsonStr, err := x.storage.Get(ctx, lockId)
			if err != nil {
				continue
			}
			if jsonStr == "" {
				continue
			}
			info, err := storage.LockInformationFromJsonString(jsonStr)
			if err != nil {
				continue
			}
			if info.Version == lastVersion {
				continue
			}
			lastVersion = info.Version
			select {
			case <-ctx.Done():
				return
			case <-x.closed:
				return
			case out <- info:
			}
		}
	}()

	return out, nil
}

func (x *pollingLockWatcher) Close() error {
	x.closeOnce.Do(func() {
		close(x.closed)
	})
	return nil
}
