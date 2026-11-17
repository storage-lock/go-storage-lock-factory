# Changelog

## v0.1.0
_发布日期：2026-11-18_

- build: 升级依赖到 go-storage/go-storage-lock v0.1.0

## v0.0.5
_发布日期：2026-10-23_

- build: use published lock dependency stack

## v0.0.4
_发布日期：2026-10-23_

- build: use published go-events and fix fair lock test race

## v0.0.3
_发布日期：2026-10-12_

- fix: 公平锁测试的 mock 跟上墓碑释放语义
- 更新 go-storage-lock-factory
- fix(factory): 漏洞3 nil 守卫 + 漏洞5 Shutdown 移除 bean
- fix: BeanFactory 双重检查锁失效致并发崩溃与资源泄漏(漏洞L，致命)

## v0.0.2
_发布日期：2023-08-31_

- 修正createLockWithOptions
- ci: 防止重复触发
- feat: StorageLockFactoryBeanFactory add some manage API
- feat: add StorageLockFactoryBeanFactory

## v0.0.1
_发布日期：2023-08-07_

- 完善文档
