-- Button icons (M7, D-076): an icon from the pack bundled with the app,
-- named like tabler:player-pause, or '' for none; and whether the button
-- shows only its icon. The label is kept either way: screen readers and
-- tooltips read it.
ALTER TABLE buttons ADD COLUMN icon TEXT NOT NULL DEFAULT '';
ALTER TABLE buttons ADD COLUMN hide_label INTEGER NOT NULL DEFAULT 0 CHECK (hide_label IN (0, 1));
