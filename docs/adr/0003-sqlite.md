# ADR-0003: SQLite (WAL) as the only database

Status: Accepted · 2026-09-28

Single-host, single-writer workload of a few thousand rows/day. SQLite in WAL
mode with `busy_timeout=10000`, foreign keys and `synchronous=NORMAL`;
backups use `VACUUM INTO`. No Postgres/Redis. The authoritative migrations
include session-bound FCM device tokens (0009); camera configuration stays
in YAML, outside the database.
