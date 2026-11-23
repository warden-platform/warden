-- dispatch_token 明文创哈希落库：明文仅在分发时一次性下发给 connector，数据库只存
-- sha256 哈希，防止拥有库只读权限者可回放令牌伪造执行结果。dispatch_token（UUID 列）
-- 保留（分发时短暂承载明文，随后置 NULL），最终持久化的是 dispatch_token_hash。
ALTER TABLE executions ADD COLUMN IF NOT EXISTS dispatch_token_hash TEXT;