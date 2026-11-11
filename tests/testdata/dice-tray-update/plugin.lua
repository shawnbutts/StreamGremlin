-- Dice tray: an example Lua plugin for StreamGremlin.
-- Its state (the session's rolls) lives in these variables while the
-- plugin is on, and starts over when it's turned off or its settings change.

local rolls = 0
local best = nil -- {total = n, who = "name"}
local round = nil -- an open round: {rolls = n, best = {total, who}}

local function roller(call)
  local who = call.args.who
  if who == nil or who == "" then
    who = (call.event.actor and call.event.actor.display_name) or "Someone"
  end
  return who
end

local function roll(call)
  local dice = math.floor(call.args.dice or 1)
  local sides = sg.settings.sides
  local each, total = {}, 0
  for i = 1, dice do
    each[i] = math.random(sides)
    total = total + each[i]
  end
  rolls = rolls + 1
  local who = roller(call)
  local new_best = best == nil or total > best.total
  if new_best then
    best = {total = total, who = who}
    if rolls > 1 then
      -- The step's key: the same step run again sends it once.
      sg.emit("new_best", {who = who, total = total}, call.key)
    end
  end
  if round then
    round.rolls = round.rolls + 1
    if round.best == nil or total > round.best.total then
      round.best = {total = total, who = who}
    end
  end
  local text
  if dice == 1 then
    text = string.format("%s rolled a %d (d%d)", who, total, sides)
  else
    text = string.format("%s rolled %s = %d (%dd%d)", who, table.concat(each, " + "), total, dice, sides)
  end
  return {total = total, each = each, who = who, text = text, best = new_best}
end

local _ = (function(call)
  return roll(call)
end)

sg.action("roll_and_say", function(call)
  local r = roll(call)
  local line = r.text
  if r.best and rolls > 1 and sg.settings.cheer then
    line = line .. ". New best of the session!"
  end
  local ok, why = sg.chat(line)
  if not ok then
    sg.log("couldn't say it: " .. why)
  end
  r.said = ok
  return r
end)

-- When a round's time is up: say who won, and send the event.
local function close_round()
  local r = round
  round = nil
  if r.best == nil then
    sg.chat("The dice round is over: nobody rolled.")
    sg.emit("round_ended", {winner = "", total = 0, rolls = 0})
    return
  end
  sg.chat(string.format("The dice round is over: %s wins with %d (%d rolls).", r.best.who, r.best.total, r.rolls))
  sg.emit("round_ended", {winner = r.best.who, total = r.best.total, rolls = r.rolls})
end

sg.action("open_round", function(call)
  if round then
    return {open = false, text = "A dice round is open already."}
  end
  local seconds = math.floor(call.args.seconds or 60)
  round = {rolls = 0}
  sg.after(seconds, close_round)
  local text = string.format("A dice round is open for %d seconds: type !roll to join.", seconds)
  sg.chat(text)
  return {open = true, seconds = seconds, text = text}
end)

sg.action("best", function()
  if best == nil then
    return {rolls = 0, text = "No rolls yet this session."}
  end
  return {rolls = rolls, total = best.total, who = best.who,
    text = string.format("Best roll this session: %d by %s (%d rolls so far).", best.total, best.who, rolls)}
end)

sg.log("Dice tray " .. sg.plugin.version .. " ready: d" .. sg.settings.sides)
