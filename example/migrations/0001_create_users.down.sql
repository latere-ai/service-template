-- The reverse of 0001_create_users.up.sql, for local development only.
--
-- The runner never applies a down file. A production rollback is a new forward
-- migration, because the previous release is still reading the schema while a
-- rollback runs.

DROP TABLE users;
