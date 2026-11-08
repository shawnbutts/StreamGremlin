-- Deleting a workflow (docs/adr/0010-editor-run-delete.md) marks it: its
-- versions and runs are kept and referenced, and it can be restored.
ALTER TABLE workflows ADD COLUMN deleted_at TEXT;
