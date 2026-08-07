-- This compatibility migration is intentionally irreversible. On fresh
-- databases these tables already belong to migration 001; on upgraded
-- databases dropping them would destroy Go Harness swarm data.
SELECT 1;
