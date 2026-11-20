-- 111: 移除未闭环的执行生命周期遗骸与无消费者的 Outbox。
--
-- execution_attempts：001 设计的「每次派发尝试独立记录 + 凭据租约绑尝试」
--   模型从未实现（实际退化为 executions.attempt 计数器，租约直接挂
--   execution_id），全代码零读写。credential_leases.attempt_id 外键亦零使用。
-- outbox_events：事务 outbox 只写不读，无 relay/消费者，通知已走独立异步
--   旁路（notifyApprovalAsync）。保留只会在误开启 WARDEN_OUTBOX_ENABLED 时
--   无限堆积。整体移除（PublishTx 调用与 OutboxRepo 已同步删除）；未来若需
--   事件集成再带消费者完整重建。
-- 幂等：IF EXISTS。

ALTER TABLE credential_leases DROP COLUMN IF EXISTS attempt_id;
DROP TABLE IF EXISTS execution_attempts;
DROP TABLE IF EXISTS outbox_events;