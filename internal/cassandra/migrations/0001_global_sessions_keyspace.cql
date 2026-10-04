-- 0001_global_sessions_keyspace.cql — Global Session Store schema.
--
-- Creates the global_sessions keyspace and the four core tables for the
-- Global Session Manager (hub ADR 0083). Every statement is idempotent
-- (CREATE ... IF NOT EXISTS) so the migration runner can safely replay
-- this file on every boot.
--
-- Keyspace: global_sessions on the shared Cassandra cluster
-- (192.168.31.162:9042). Logical isolation by keyspace — no separate
-- cluster is provisioned (ADR 0033 pattern).

CREATE KEYSPACE IF NOT EXISTS global_sessions
  WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

-- scope_registry — which Nexus instances (and global services) are
-- registered to publish session metadata. One row per scope.
CREATE TABLE IF NOT EXISTS global_sessions.scope_registry (
    scope_id              text PRIMARY KEY,
    account_id            uuid,
    nexus_instance_id     uuid,
    base_service_version  int,
    endpoint              text,
    credential_id         text,
    last_heartbeat        timestamp,
    status                text
);

-- session_index — metadata index of every session the GSM knows about.
-- Partitioned by scope_id so sessions of different scopes cannot collide
-- or leak. Nexus-scoped sessions have metadata only (content stays at the
-- edge); global-scoped sessions have full content in global_session_events.
CREATE TABLE IF NOT EXISTS global_sessions.session_index (
    scope_id       text,
    session_id     uuid,
    title          text,
    created_at     timestamp,
    updated_at     timestamp,
    turn_count     int,
    event_count    int,
    content_digest text,
    status         text,
    PRIMARY KEY ((scope_id), session_id)
);

-- global_session_events — the canonical append-only event log for
-- global-scope sessions only (Conclave cvsess-*, Medium Mode, etc.).
-- Nexus-scoped session events live in keyspace risegen on the originating
-- Nexus, never here.
CREATE TABLE IF NOT EXISTS global_sessions.global_session_events (
    scope_id    text,
    session_id  uuid,
    seq         bigint,
    event_id    uuid,
    event_type  text,
    turn_id     uuid,
    payload     text,
    ts          timestamp,
    PRIMARY KEY ((scope_id, session_id), seq)
) WITH CLUSTERING ORDER BY (seq ASC);

-- rejected_inbound — events from incompatible major versions, never
-- silently dropped. Partitioned by scope_id, clustered by ts DESC so
-- the most recent rejections are cheapest to query.
CREATE TABLE IF NOT EXISTS global_sessions.rejected_inbound (
    scope_id   text,
    ts         timestamp,
    event_id   uuid,
    reason     text,
    payload    text,
    PRIMARY KEY ((scope_id), ts, event_id)
) WITH CLUSTERING ORDER BY (ts DESC, event_id ASC);