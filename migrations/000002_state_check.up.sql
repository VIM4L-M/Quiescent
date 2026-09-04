ALTER TABLE mandate_cycles
  ADD CONSTRAINT state_valid CHECK (state IN
    ('pending', 'scheduled', 'in_flight', 'unknown', 'held', 'recovered', 'escalated', 'abandoned'));
