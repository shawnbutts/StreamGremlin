-- Moderating YouTube (M6 slice 5, D-100): what YouTube made, a ban's or a
-- moderator's id, kept so StreamGremlin can undo it. YouTube lists
-- neither bans nor (cheaply) moderators, so without it an unban isn't
-- possible. '' for Twitch and for anything that made nothing.
ALTER TABLE moderation ADD COLUMN result_id TEXT NOT NULL DEFAULT '';
