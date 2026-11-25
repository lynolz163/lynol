ALTER TABLE public.notification_channels ADD COLUMN IF NOT EXISTS org_managed BOOLEAN NOT NULL DEFAULT false;

-- Private undo state, never returned by the audit API. Credentials stay in the
-- database just as in notification_channels; public audit entries contain keys only.
CREATE TABLE IF NOT EXISTS org.notification_channel_change (
 operation_id UUID NOT NULL REFERENCES org.change_operation(id),
 channel_id BIGINT NOT NULL,
 before_value JSONB,
 after_updated_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(operation_id,channel_id)
);
