# Dice tray

An example Lua plugin for StreamGremlin ([ADR-0038](../../../docs/adr/0038-lua-plugins.md)).

It adds four steps to workflows:

- **Roll dice:** rolls 1 to 10 dice and gives back `total`, `each`, `who` and `text` ("Fan rolled a 4 (d6)") for later steps to use.
- **Roll dice and say it in chat:** the same, and says it in chat itself (it asks for the `chat.send` permission), cheering a new best roll of the session.
- **Open a dice round:** opens a round for 10 to 600 seconds. Rolls in it count, and when its time is up (a timer, `sg.after`), it says who rolled highest in chat.
- **The session's best roll:** who rolled highest since the plugin was turned on.

It sends two events, to start workflows (it asks for the `events.publish` permission): **A dice round ends**, with the winner, their roll and how many rolled, and **A new best roll**. In the workflow editor they're under **When**, in the **Dice tray (add-on)** group.

Settings: the sides on each die, and whether to cheer a new best roll.

It adds four templates to the gallery, in their own **Dice tray** section: **!roll** (rolls dice and says it), **!best** (the session's best roll), **!diceround** (mods open a 60-second round) and **Cheer a dice round's winner** (started by its event). Templates are in `templates/`, one JSON file each, in the same format as StreamGremlin's own.

The session's rolls, and an open round with its timer, live in the script's own variables, so they start over when the plugin is turned off, its settings change, or StreamGremlin restarts.

To install it, compress this folder into a ZIP and pick it on **Add-ons**.
