-- Buttons (M7, D-075): the Live screen's buttons, and later the Stream
-- Deck's. Pressing one records a buttons.button.pressed event; workflows
-- match its button_id. A button holds no steps of its own.
CREATE TABLE buttons (
	id         TEXT PRIMARY KEY,
	label      TEXT NOT NULL,
	color      TEXT NOT NULL DEFAULT '', -- '' or one of the app's named colours
	position   INTEGER NOT NULL,         -- order on the Live screen, from 0
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
) STRICT;
