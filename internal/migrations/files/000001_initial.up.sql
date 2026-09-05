CREATE TABLE mandate_cycles (
    cycle_id        UUID PRIMARY KEY,
    mandate_id      UUID NOT NULL,
    customer_id     UUID NOT NULL,
    rail            TEXT NOT NULL,
    amount_paise    BIGINT NOT NULL,
    due_date        DATE NOT NULL,
    attempts_used   SMALLINT NOT NULL DEFAULT 0,
    state           TEXT NOT NULL,
    disposition     TEXT,
    version         BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT budget_cap   CHECK (attempts_used <= 4),
    CONSTRAINT budget_floor CHECK (attempts_used >= 0)
);

CREATE TABLE attempts (
    attempt_id      UUID PRIMARY KEY,
    cycle_id        UUID NOT NULL REFERENCES mandate_cycles,
    seq             SMALLINT NOT NULL,
    idempotency_key TEXT NOT NULL UNIQUE,
    fence           BIGINT,
    scheduled_for   TIMESTAMPTZ NOT NULL,
    fired_at        TIMESTAMPTZ,
    outcome         TEXT,
    failure_code    TEXT,
    decision_reason JSONB NOT NULL,
    UNIQUE (cycle_id, seq)
);

CREATE INDEX attempts_due ON attempts (scheduled_for) WHERE outcome IS NULL;

CREATE TABLE leases (
    cycle_id   UUID PRIMARY KEY REFERENCES mandate_cycles,
    holder     TEXT,
    fence      BIGINT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch'
);

CREATE TABLE outbox (
    id           BIGSERIAL PRIMARY KEY,
    cycle_id     UUID NOT NULL REFERENCES mandate_cycles,
    attempt_id   UUID NOT NULL REFERENCES attempts,
    kind         TEXT NOT NULL,
    payload      JSONB NOT NULL,
    deliver_by   TIMESTAMPTZ NOT NULL,
    delivered_at TIMESTAMPTZ,
    attempts     SMALLINT NOT NULL DEFAULT 0
);

CREATE INDEX outbox_pending ON outbox (deliver_by) WHERE delivered_at IS NULL;

CREATE INDEX outbox_notice_lookup ON outbox (attempt_id, kind);

CREATE TABLE audit_log (
    id             BIGSERIAL PRIMARY KEY,
    cycle_id       UUID NOT NULL,
    correlation_id UUID NOT NULL,
    at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    event          TEXT NOT NULL,
    inputs         JSONB NOT NULL,
    decision       JSONB NOT NULL,
    reason         TEXT NOT NULL
);

CREATE FUNCTION seed_lease() RETURNS trigger AS $$
BEGIN
  INSERT INTO leases (cycle_id) VALUES (NEW.cycle_id);
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER seed_lease_on_cycle
  AFTER INSERT ON mandate_cycles
  FOR EACH ROW EXECUTE FUNCTION seed_lease();

CREATE FUNCTION touch_updated_at() RETURNS trigger AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END $$ LANGUAGE plpgsql;

CREATE TRIGGER touch_mandate_cycles_updated_at
  BEFORE UPDATE ON mandate_cycles
  FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
