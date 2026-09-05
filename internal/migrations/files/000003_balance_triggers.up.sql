CREATE TABLE balance_triggers (
    trigger_id   UUID PRIMARY KEY,
    cycle_id     UUID NOT NULL REFERENCES mandate_cycles,
    seq          SMALLINT NOT NULL,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    responded_at TIMESTAMPTZ,
    response     TEXT,
    CONSTRAINT balance_triggers_response_valid CHECK (response IN ('yes', 'no')),
    UNIQUE (cycle_id, seq)
);

CREATE INDEX balance_triggers_pending ON balance_triggers (cycle_id) WHERE responded_at IS NULL;
