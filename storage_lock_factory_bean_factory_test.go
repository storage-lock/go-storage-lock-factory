package storage_lock_factory

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"
)

const testKey = "test"

func TestStorageLockFactoryBeanFactory_GetBean(t *testing.T) {

	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	// 未初始化时应该获取不到
	bean, b := beanFactory.GetBean(testKey)
	assert.False(t, b)
	assert.Nil(t, bean)

	// 初始化
	beanFactory.InitBean(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})

	// 初始化完之后获取到了
	bean, b = beanFactory.GetBean(testKey)
	assert.True(t, b)
	assert.NotNil(t, bean)

}

func TestStorageLockFactoryBeanFactory_GetOrInit(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	// 第一次获取
	factory1, err := beanFactory.GetOrInit(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})
	assert.Nil(t, err)
	assert.NotNil(t, factory1)

	// 第二次获取
	factory2, err := beanFactory.GetOrInit(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})
	assert.Nil(t, err)
	assert.NotNil(t, factory2)

	// 期望两次获取到的应该是同一个值
	assert.Equal(t, factory1, factory2)

}

func TestStorageLockFactoryBeanFactory_InitBean(t *testing.T) {
	beanFactory := NewStorageLockFactoryBeanFactory[string, string]()

	// 未初始化时应该获取不到
	bean, b := beanFactory.GetBean(testKey)
	assert.False(t, b)
	assert.Nil(t, bean)

	// 初始化
	beanFactory.InitBean(context.Background(), testKey, func(ctx context.Context) (*StorageLockFactory[string], error) {
		factory := NewStorageLockFactory[string](nil, nil)
		return factory, nil
	})

	// 初始化完之后获取到了
	bean, b = beanFactory.GetBean(testKey)
	assert.True(t, b)
	assert.NotNil(t, bean)

}
