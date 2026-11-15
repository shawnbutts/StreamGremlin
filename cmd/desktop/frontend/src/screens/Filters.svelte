<script lang="ts">
  // Chat filters (D-085): six rules that catch spam and delete, time out or
  // warn, each sent once as you and recorded; a box to try a message; and
  // what the filters caught lately, with Twitch's answer. And Twitch's own
  // Blocked Terms, with copying words either way (D-088).
  import { onMount } from 'svelte';
  import {
    ApiError,
    addBlockedTerms,
    checkFilter,
    getBlockedTerms,
    removeBlockedTerm,
    type BlockedTerm,
    getFilters,
    getRaidGuard,
    setRaidGuard,
    testRaidGuard,
    type RaidGuardSettings,
    type RaidGuardView,
    saveFilters,
    type FilterCatch,
    type FilterRule,
    type FilterRuleSettings,
    type FilterSettings,
  } from '../lib/api';
  import { eventTime } from '../lib/events';

  let { refreshMs = 5000 }: { refreshMs?: number } = $props();

  const message = (e: unknown) => (e instanceof ApiError ? e.message : String(e));

  // The form, read once so a refresh never undoes what's being typed.
  let form = $state<FilterSettings | undefined>();
  let wordsText = $state('');
  let allowedText = $state('');
  let recent = $state<FilterCatch[]>([]);
  let loadError = $state('');
  let busy = $state(false);
  let notice = $state('');
  let error = $state('');
  let tryText = $state('');
  let tryRole = $state('viewer');
  let tryResult = $state('');

  const order: FilterRule[] = ['words', 'links', 'emotes', 'caps', 'symbols', 'repeats'];
  const ruleName: Record<FilterRule, string> = {
    words: 'Words',
    links: 'Links',
    emotes: 'Emotes',
    caps: 'Capitals',
    symbols: 'Symbols',
    repeats: 'Repeats',
  };
  const ruleHelp: Record<FilterRule, string> = {
    words: 'Your words and phrases, as whole words, in any case. A * at the start or end matches the rest of a word: spoil* catches spoiler.',
    links: 'Web addresses, with or without http or www. Allowed sites and their subdomains never count. Mods let one viewer post links with !permit (the “Permit a link” template).',
    emotes: 'Too many emotes in one message.',
    caps: 'Messages mostly in capitals. Emotes’ names don’t count.',
    symbols: 'Messages mostly of symbols, like !!!!! or ░░░░.',
    repeats: 'The same message from the same viewer again and again within a minute, ignoring case and spaces.',
  };
  const actionWords: Record<string, string> = { delete: 'deleted', timeout: 'timed out', warn: 'warned' };
  const stateWords: Record<FilterCatch['state'], string> = { done: 'Done', refused: 'Refused', unknown: 'Unknown', test: 'Test: nothing sent' };

  const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean);

  function fill(s: FilterSettings) {
    form = structuredClone(s);
    wordsText = s.words.list.join('\n');
    allowedText = s.links.allowed.join('\n');
  }

  async function load() {
    try {
      const v = await getFilters();
      recent = v.recent;
      if (!form) fill(v.settings);
      loadError = '';
    } catch (e) {
      loadError = message(e);
    }
  }

  // The raid guard (D-107): read once, saved on its own button.
  let guard = $state<RaidGuardSettings | undefined>();
  let guardBusy = $state(false);
  let guardNotice = $state('');
  let guardError = $state('');

  async function loadGuard() {
    try {
      guard = (await getRaidGuard()).settings;
    } catch (e) {
      guardError = message(e);
    }
  }

  async function guardAct(f: () => Promise<RaidGuardView>, done: string) {
    guardBusy = true;
    guardNotice = '';
    guardError = '';
    try {
      guard = (await f()).settings;
      guardNotice = done;
    } catch (e) {
      guardError = message(e);
    } finally {
      guardBusy = false;
    }
  }

  onMount(() => {
    load();
    loadBlocked();
    loadGuard();
    const t = setInterval(load, refreshMs);
    return () => clearInterval(t);
  });

  // The form as settings, with each length only on a timeout.
  function settings(): FilterSettings {
    const s = structuredClone($state.snapshot(form)) as FilterSettings;
    s.words.list = lines(wordsText);
    s.links.allowed = lines(allowedText);
    for (const r of order) {
      const rule = s[r] as FilterRuleSettings;
      rule.seconds = rule.action === 'timeout' ? Number(rule.seconds) || 0 : 0;
    }
    for (const [k, f] of [['emotes', 'max'], ['caps', 'min_letters'], ['caps', 'percent'], ['symbols', 'min_length'], ['symbols', 'percent'], ['repeats', 'times']] as const) {
      (s[k] as unknown as Record<string, number>)[f] = Number((s[k] as unknown as Record<string, number>)[f]);
    }
    return s;
  }

  function setAction(r: FilterRule, a: string) {
    const rule = form![r] as FilterRuleSettings;
    rule.action = a as FilterRuleSettings['action'];
    if (a === 'timeout' && !rule.seconds) rule.seconds = 600;
  }

  async function save(e: SubmitEvent) {
    e.preventDefault();
    busy = true;
    error = '';
    notice = '';
    try {
      const v = await saveFilters(settings());
      fill(v.settings);
      recent = v.recent;
      const on = order.filter((r) => v.settings[r].on).length;
      notice = !v.settings.on
        ? 'Saved. The filters are off.'
        : on === 0
          ? 'Saved. The filters are on, but no filter is: tick the ones you want.'
          : `Saved. ${on} filter${on === 1 ? ' is' : 's are'} watching chat from now on.`;
    } catch (err) {
      error = message(err);
    } finally {
      busy = false;
    }
  }

  // Twitch's Blocked Terms: read when the screen opens and on Refresh,
  // never every few seconds, since each read is a request to Twitch.
  let blocked = $state<BlockedTerm[] | undefined>();
  let blockedProblem = $state('');
  let blockedBusy = $state(false);
  let blockedNotice = $state('');
  let blockedError = $state('');

  async function loadBlocked() {
    try {
      const v = await getBlockedTerms();
      blocked = v.terms;
      blockedProblem = v.problem ?? '';
    } catch (e) {
      blockedProblem = message(e);
    }
  }

  const onTwitch = $derived(new Set((blocked ?? []).map((t) => t.text.toLowerCase())));
  const myWords = $derived(lines(wordsText).map((w) => w.toLowerCase()));
  const toTwitch = $derived([...new Set(myWords)].filter((w) => !onTwitch.has(w)));
  const toWords = $derived([...new Set((blocked ?? []).map((t) => t.text.toLowerCase()))].filter((t) => !myWords.includes(t)));
  const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

  async function copyToTwitch() {
    blockedBusy = true;
    blockedNotice = '';
    blockedError = '';
    try {
      const { results } = await addBlockedTerms(toTwitch);
      const done = results.filter((r) => r.state === 'done').length;
      const rest = results.filter((r) => r.state !== 'done').map((r) => `${r.text}: ${r.state === 'unknown' ? 'unknown' : 'refused'}, ${r.answer}`);
      blockedNotice = `Added ${plural(done, 'word', 'words')} to Twitch’s Blocked Terms.`;
      if (rest.length) blockedError = `Not added: ${rest.join('; ')}`;
      await loadBlocked();
    } catch (e) {
      blockedError = message(e);
    } finally {
      blockedBusy = false;
    }
  }

  function copyToWords() {
    const n = toWords.length;
    wordsText = [...lines(wordsText), ...toWords].join('\n');
    if (form && !form.words.on) form.words.on = true;
    blockedNotice = `Put ${plural(n, 'term', 'terms')} in Words above. Save to keep ${n === 1 ? 'it' : 'them'}.`;
    blockedError = '';
  }

  async function unblock(t: BlockedTerm) {
    blockedBusy = true;
    blockedNotice = '';
    blockedError = '';
    try {
      await removeBlockedTerm(t.id);
      blockedNotice = `Removed “${t.text}” from Twitch’s Blocked Terms.`;
      await loadBlocked();
    } catch (e) {
      blockedError = message(e);
    } finally {
      blockedBusy = false;
    }
  }

  const until = (at: string) => new Date(at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });

  async function tryIt(e: SubmitEvent) {
    e.preventDefault();
    tryResult = '';
    error = '';
    try {
      const v = await checkFilter(tryText, tryRole, settings());
      tryResult = v.rule ? `${ruleName[v.rule]} would catch it: “${v.reason}”` : 'No filter that’s ticked would catch it.';
    } catch (err) {
      error = message(err);
    }
  }
</script>

<h1>Chat filters</h1>
<p class="sg-muted screen-intro">
  Catch spam in your Twitch and YouTube chat and delete it, time the viewer out or warn them, as you. You, your moderators and your bot
  are never filtered. Each action is sent once and listed below with the answer. On YouTube a filter deletes (20 quota units) unless you
  say otherwise: YouTube has no warnings, and a timeout costs 200 units. Twitch’s Blocked Terms are below; its AutoMod is
  on Twitch, under Creator Dashboard, Settings, Moderation, and acts before StreamGremlin sees a message.
</p>

{#if loadError}
  <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {loadError}</p>
{/if}

{#if form}
  <section class="sg-panel card" aria-labelledby="filters-heading" aria-busy={busy}>
    <h2 id="filters-heading">Filters</h2>
    <form onsubmit={save} aria-label="Chat filters">
      <label class="choice-inline"><input type="checkbox" bind:checked={form.on} /> Filters are on</label>
      {#each order as r (r)}
        {@const rule = form[r] as FilterRuleSettings}
        <fieldset>
          <legend>{ruleName[r]}</legend>
          <p class="sg-muted">{ruleHelp[r]}</p>
          <label class="choice-inline"><input type="checkbox" bind:checked={rule.on} /> Filter {ruleName[r].toLowerCase()}</label>
          {#if rule.on}
            <div class="row-actions">
              <label class="field inline-field"
                ><span>What happens</span>
                <select class="sg-input" value={rule.action} onchange={(e) => setAction(r, e.currentTarget.value)}>
                  <option value="delete">Delete the message</option>
                  <option value="timeout">Time them out</option>
                  <option value="warn">Warn them</option>
                </select></label
              >
              {#if rule.action === 'timeout'}
                <label class="field inline-field"
                  ><span>For (seconds)</span><input class="sg-input" type="number" min="1" max="1209600" bind:value={rule.seconds} /></label
                >
              {/if}
              {#if r === 'emotes'}
                <label class="field inline-field"><span>More than (emotes)</span><input class="sg-input" type="number" min="1" max="100" bind:value={form.emotes.max} /></label>
              {:else if r === 'caps'}
                <label class="field inline-field"><span>At least (letters)</span><input class="sg-input" type="number" min="1" max="500" bind:value={form.caps.min_letters} /></label>
                <label class="field inline-field"><span>Capitals (%)</span><input class="sg-input" type="number" min="1" max="100" bind:value={form.caps.percent} /></label>
              {:else if r === 'symbols'}
                <label class="field inline-field"><span>At least (characters)</span><input class="sg-input" type="number" min="1" max="500" bind:value={form.symbols.min_length} /></label>
                <label class="field inline-field"><span>Symbols (%)</span><input class="sg-input" type="number" min="1" max="100" bind:value={form.symbols.percent} /></label>
              {:else if r === 'repeats'}
                <label class="field inline-field"><span>Times in a minute</span><input class="sg-input" type="number" min="2" max="10" bind:value={form.repeats.times} /></label>
              {/if}
            </div>
            {#if rule.action === 'warn'}
              <p class="sg-muted">A warning shows the viewer why, and they must accept it before chatting again. On YouTube, which has no warnings, the message is deleted.</p>
            {:else if rule.action === 'timeout'}
              <label class="choice-inline"
                ><input type="checkbox" bind:checked={rule.youtube_same} /> Time out on YouTube too (200 quota units each; otherwise it deletes there, and
                it always deletes once fewer than 1,000 units are left today)</label
              >
            {/if}
            {#if r === 'words'}
              <label class="field"><span>Words and phrases, one a line</span><textarea class="sg-input" rows="4" bind:value={wordsText}></textarea></label>
            {:else if r === 'links'}
              <label class="field"><span>Allowed sites, one a line (like twitch.tv)</span><textarea class="sg-input" rows="3" bind:value={allowedText}></textarea></label>
            {/if}
            <div class="row-actions">
              <label class="choice-inline"><input type="checkbox" bind:checked={rule.exempt_vips} /> Leave VIPs alone</label>
              <label class="choice-inline"><input type="checkbox" bind:checked={rule.exempt_subscribers} /> Leave subscribers alone</label>
            </div>
          {/if}
        </fieldset>
      {/each}
      <button type="submit" class="sg-button" disabled={busy}>Save</button>
    </form>
    <p class="saved" role="status">{notice}</p>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
  </section>

  <section class="sg-panel card" aria-labelledby="try-heading">
    <h2 id="try-heading">Try a message</h2>
    <p class="sg-muted">Judged with the form above, saved or not, as if the filters were on. Nothing is sent. Repeats need several messages, so they’re not tried here.</p>
    <form onsubmit={tryIt} aria-label="Try a message" class="row-actions">
      <label class="field inline-field grow"><span>Message</span><input class="sg-input" maxlength="500" bind:value={tryText} /></label>
      <label class="field inline-field"
        ><span>From</span>
        <select class="sg-input" bind:value={tryRole}>
          <option value="viewer">a viewer</option>
          <option value="subscriber">a subscriber</option>
          <option value="vip">a VIP</option>
          <option value="moderator">a moderator</option>
        </select></label
      >
      <button type="submit" class="sg-button" disabled={!tryText.trim()}>Try</button>
    </form>
    <p role="status">{tryResult}</p>
  </section>
{/if}

{#if form}
  <section class="sg-panel card" aria-labelledby="blocked-heading" aria-busy={blockedBusy}>
    <div class="head-row">
      <h2 id="blocked-heading">Twitch’s Blocked Terms</h2>
      <button type="button" class="button" onclick={loadBlocked} disabled={blockedBusy}>Refresh</button>
    </div>
    <p class="sg-muted">
      Twitch holds these messages back before anyone sees them, StreamGremlin included, so nothing is deleted or recorded here. Copying adds
      only; it never removes anything from either list.
    </p>
    {#if blockedProblem}
      <p class="state state-unknown" role="alert"><span aria-hidden="true">?</span> Unknown: {blockedProblem}</p>
    {:else if !blocked}
      <p class="sg-muted">Asking Twitch…</p>
    {:else}
      <div class="row-actions">
        <button type="button" class="button" onclick={copyToTwitch} disabled={blockedBusy || toTwitch.length === 0}
          >Copy my words to Twitch ({toTwitch.length} new)</button
        >
        <button type="button" class="button" onclick={copyToWords} disabled={blockedBusy || toWords.length === 0}
          >Copy Twitch’s terms to my words ({toWords.length} new)</button
        >
      </div>
      {#if blocked.length === 0}
        <p class="sg-muted">The list is empty.</p>
      {:else}
        <ul class="blocked">
          {#each blocked as t (t.id)}
            <li>
              <span class="text"
                >{t.text}{#if t.expires_at}{' '}<span class="sg-muted until">AutoMod, until {until(t.expires_at)}</span>{/if}</span
              >
              <button type="button" class="button" onclick={() => unblock(t)} disabled={blockedBusy} aria-label={`Remove ${t.text}`}>Remove</button>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
    <p class="saved" role="status">{blockedNotice}</p>
    {#if blockedError}<p class="error" role="alert">{blockedError}</p>{/if}
  </section>
{/if}

{#if guard}
  {@const g = guard}
  <section class="sg-panel card" aria-labelledby="guard-heading" aria-busy={guardBusy}>
    <h2 id="guard-heading">Raid guard</h2>
    <p class="sg-muted">
      Notices a hate raid as it starts: a burst of chatters StreamGremlin has never seen, or of Twitch accounts made only days ago.
      You, your mods, VIPs and subscribers, and viewers it already knows never count. It warns you on Live and starts “A raid looks
      suspicious”; it locks chat only if you choose that, and never unlocks on its own.
    </p>
    <form class="test-form" aria-label="Raid guard settings" onsubmit={(e) => {
      e.preventDefault();
      guardAct(() => setRaidGuard(g), 'Saved.');
    }}>
      <fieldset class="field">
        <legend>When it sees a burst</legend>
        <label class="field-check"><input type="radio" name="guard-mode" value="alert" bind:group={g.mode} /> Warn me on Live</label>
        <label class="field-check"><input type="radio" name="guard-mode" value="lock" bind:group={g.mode} /> Warn me and lock chat (Shield Mode, emote-only, followers-only)</label>
        <label class="field-check"><input type="radio" name="guard-mode" value="off" bind:group={g.mode} /> Off</label>
      </fieldset>
      <label class="field"><span>First-time chatters in a burst</span><input class="sg-input" type="number" min="2" max="100" bind:value={g.first_time} /></label>
      <label class="field"><span>Brand-new Twitch accounts in a burst</span><input class="sg-input" type="number" min="2" max="100" bind:value={g.new_accounts} /></label>
      <label class="field"><span>Brand-new means younger than (days)</span><input class="sg-input" type="number" min="1" max="365" bind:value={g.days} /></label>
      <label class="field"><span>Within (seconds)</span><input class="sg-input" type="number" min="10" max="600" bind:value={g.window_seconds} /></label>
      <label class="field">
        <span>Watch more closely after a raid (minutes)</span>
        <input class="sg-input" type="number" min="0" max="30" bind:value={g.after_raid_minutes} />
        <span class="sg-muted field-hint">For this long after a raid arrives, half as many is a burst. 0: no closer watch.</span>
      </label>
      <div class="row-actions">
        <button type="submit" class="sg-button" disabled={guardBusy}>Save</button>
        <button type="button" class="button" disabled={guardBusy} onclick={() => guardAct(testRaidGuard, 'A test warning is on Live. Nothing was locked.')}
          >Test the warning</button
        >
      </div>
    </form>
    <p class="saved" role="status">{guardNotice}</p>
    {#if guardError}<p class="error" role="alert">{guardError}</p>{/if}
  </section>
{/if}

<section class="sg-panel card" aria-labelledby="caught-heading">
  <h2 id="caught-heading">Caught lately</h2>
  {#if recent.length === 0}
    <p class="sg-muted">Nothing yet.</p>
  {:else}
    <ul class="caught">
      {#each recent as c, i (i)}
        <li>
          <span class="sg-muted">{eventTime(c.at)}</span>
          <strong>{c.name || c.login}</strong>{c.platform === 'youtube' ? ' on YouTube' : ''}
          {actionWords[c.action]}{c.action === 'timeout' && c.seconds ? ` for ${c.seconds} s` : ''} by {ruleName[c.rule]}:
          <span class="text">{c.text}</span>
          <span class="answer">{stateWords[c.state]}{c.answer ? `: ${c.answer}` : ''}{c.note ? ` (${c.note})` : ''}</span>
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  fieldset { border: none; padding: 0; margin: 0 0 1rem; }
  legend { font-weight: 600; margin-bottom: 0.25rem; }
  fieldset p { margin: 0 0 0.25rem; }
  .grow { flex: 1; }
  .caught { list-style: none; padding: 0; margin: 0; display: grid; gap: 0.4rem; }
  .text { overflow-wrap: anywhere; }
  .answer { font-weight: 650; }
  .head-row { display: flex; align-items: baseline; justify-content: space-between; gap: 0.5rem; }
  .blocked { list-style: none; padding: 0; margin: 0.75rem 0 0; display: grid; gap: 0.3rem; }
  .blocked li { display: flex; align-items: center; justify-content: space-between; gap: 0.75rem; padding: 0.2rem 0; border-bottom: 1px solid var(--sg-border); }
  .blocked li:last-child { border-bottom: none; }
  .until { margin-left: 0.75rem; font-size: 0.9rem; }
  /* An empty notice takes no room, but stays in place to be announced. */
  .saved:empty { margin: 0; min-height: 0; }
</style>
