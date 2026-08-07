-- Development rollback only. Production schema changes use forward migrations.
DROP TABLE IF EXISTS agent_swarm_message_receipts;
DROP TABLE IF EXISTS agent_swarm_messages;
DROP TABLE IF EXISTS agent_swarm_team_members;
DROP TABLE IF EXISTS agent_swarm_teams;
DROP TABLE IF EXISTS skill_usage;
DROP TABLE IF EXISTS memory_fact;
DROP TABLE IF EXISTS agent_run_completion;
DROP TABLE IF EXISTS agent_run_event;
DROP TABLE IF EXISTS agent_run;
DROP TABLE IF EXISTS agent_message;
DROP TABLE IF EXISTS agent_thread;
