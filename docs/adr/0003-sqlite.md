# ADR-0003: SQLite (WAL) as the only database

Status: Accepted · 2026-09-28

Single-host, single-writer workload of a few thousand rows/day. SQLite in WAL
mode with `busy_timeout=5000`, backups via `VACUUM INTO`. No Postgres/Redis.
