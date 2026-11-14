-- Linked viewers (M6 slice 6, D-101, docs/adr/0048-viewer-links.md): a
-- Twitch viewer and a YouTube viewer proven to be one person, who share one
-- points balance. The ledger reads it: points owned by a linked YouTube
-- viewer are the Twitch viewer's. A link is never deleted; unlinking stamps
-- it, so the record says who shared when.
CREATE TABLE viewer_links (
	id             TEXT PRIMARY KEY,                  -- l_<hex>
	twitch_viewer  TEXT NOT NULL REFERENCES viewers (id),
	youtube_viewer TEXT NOT NULL REFERENCES viewers (id),
	linked_at      TEXT NOT NULL,
	linked_by      TEXT NOT NULL,                     -- chat, or who in the app or CLI
	unlinked_at    TEXT,
	unlinked_by    TEXT NOT NULL DEFAULT ''
) STRICT;
-- One link at a time for each viewer.
CREATE UNIQUE INDEX viewer_links_twitch ON viewer_links (twitch_viewer) WHERE unlinked_at IS NULL;
CREATE UNIQUE INDEX viewer_links_youtube ON viewer_links (youtube_viewer) WHERE unlinked_at IS NULL;
CREATE TRIGGER viewer_links_kept BEFORE DELETE ON viewer_links
	BEGIN SELECT RAISE(ABORT, 'a link is kept: unlink it instead'); END;
CREATE TRIGGER viewer_links_fixed BEFORE UPDATE OF id, twitch_viewer, youtube_viewer, linked_at, linked_by ON viewer_links
	BEGIN SELECT RAISE(ABORT, 'a link never changes, except to end'); END;

-- Giveaway messages go to Twitch or YouTube chat, one row each (D-101).
ALTER TABLE giveaway_messages ADD COLUMN platform TEXT NOT NULL DEFAULT 'twitch';
