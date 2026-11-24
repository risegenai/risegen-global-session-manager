CREATE TABLE IF NOT EXISTS global_sessions.permission_decisions (
    session_id text,
    request_id text,
    decision text,
    decided_at timestamp,
    PRIMARY KEY (session_id, request_id)
);
