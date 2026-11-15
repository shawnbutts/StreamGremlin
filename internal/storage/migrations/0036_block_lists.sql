-- Block lists (M8 slice 5, D-109): a ban list can block accounts instead
-- (the follow-bot review), with the same states, pace and undo.
ALTER TABLE ban_lists ADD COLUMN kind TEXT NOT NULL DEFAULT 'ban'; -- ban or block
