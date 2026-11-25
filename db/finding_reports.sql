CREATE TABLE IF NOT EXISTS finding_report_schedules (
    kind TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT false,
    weekday INTEGER NOT NULL DEFAULT 1,
    monthday INTEGER NOT NULL DEFAULT 1,
    hour INTEGER NOT NULL DEFAULT 9,
    minute INTEGER NOT NULL DEFAULT 0,
    channel_id BIGINT REFERENCES notification_channels(id) ON DELETE SET NULL,
    filter JSONB NOT NULL DEFAULT '{"severity":"","business_line":"","assignee":""}'
);
INSERT INTO finding_report_schedules(kind) VALUES ('weekly'),('monthly') ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS finding_reports (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    generated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    filter JSONB NOT NULL,
    current_payload JSONB NOT NULL,
    previous_payload JSONB NOT NULL,
    event_id BIGINT REFERENCES notification_events(id) ON DELETE SET NULL,
    UNIQUE(kind,period_start,filter)
);
