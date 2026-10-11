-- 容忍次数：连续收到多少个失败信号仍不判宕，0 表示第一次失败即宕机。
ALTER TABLE checks ADD COLUMN fail_tolerance INTEGER NOT NULL DEFAULT 0;
-- 当前连续失败信号计数，收到成功信号清零。
ALTER TABLE checks ADD COLUMN fail_count INTEGER NOT NULL DEFAULT 0;
-- 抖动状态：1 表示处于抖动窗口内，逐条宕机/恢复告警已被合并。
ALTER TABLE checks ADD COLUMN flapping INTEGER NOT NULL DEFAULT 0;
ALTER TABLE checks ADD COLUMN flap_since INTEGER NULL;
