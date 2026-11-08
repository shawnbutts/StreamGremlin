-- Retention (docs/adr/0012-backups-and-retention.md) removes events and
-- finished runs by age; these indexes keep the sweep from scanning.
CREATE INDEX events_received ON events (received_at);
CREATE INDEX jobs_finished ON jobs (finished_at) WHERE finished_at IS NOT NULL;
