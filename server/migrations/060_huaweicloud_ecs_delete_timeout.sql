-- Huawei Cloud may need several minutes to remove an ECS and its attached disks.
-- Keep the dispatch lease alive long enough to observe the terminal 404.
UPDATE action_definitions
SET timeout_seconds = 900
WHERE name = 'huawei.ecs.delete'
  AND version = 1;
