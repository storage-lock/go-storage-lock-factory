package storage_lock_factory

import (
	"testing"

	"github.com/storage-lock/go-storage"
)

func TestCompile_LockWatchableTypeExists(t *testing.T) {
	var _ storage.LockWatchable
}
