# Dice tray

An example Lua plugin for StreamGremlin ([ADR-0038](../../../docs/adr/0038-lua-plugins.md)).

It adds three steps to workflows:

- **Roll dice:** rolls 1 to 10 dice and gives back `total`, `each`, `who` and `text` ("Fan rolled a 4 (d6)") for later steps to use.
- **Roll dice and say it in chat:** the same, and says it in chat itself (it asks for the `chat.send` permission), cheering a new best roll of the session.
- **The session's best roll:** who rolled highest since the plugin was turned on.

Settings: the sides on each die, and whether to cheer a new best roll.

It adds two templates to the gallery, in their own **Dice tray** section: **!roll** (rolls dice and says it) and **!best** (the session's best roll). Templates are in `templates/`, one JSON file each, in the same format as StreamGremlin's own.

The session's rolls live in the script's own variables, so they start over when the plugin is turned off, its settings change, or StreamGremlin restarts.

To install it, compress this folder into a ZIP and pick it on **Add-ons**.
