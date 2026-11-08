<script lang="ts">
  // The app's own "are you sure?" dialog. AGENTS.md: never window.confirm.
  // Focus starts on Cancel, stays inside the dialog, and returns to where it
  // was when the dialog closes. Escape cancels.
  import { tick } from 'svelte';

  let {
    title,
    message,
    confirmLabel,
    onconfirm,
    oncancel,
  }: {
    title: string;
    message: string;
    confirmLabel: string;
    onconfirm: () => void;
    oncancel: () => void;
  } = $props();

  let box: HTMLDivElement;
  let cancelBtn: HTMLButtonElement;
  const opener = document.activeElement as HTMLElement | null;

  $effect(() => {
    tick().then(() => cancelBtn?.focus());
    return () => opener?.focus();
  });

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      oncancel();
      return;
    }
    if (e.key !== 'Tab') return;
    const buttons = Array.from(box.querySelectorAll<HTMLButtonElement>('button'));
    const first = buttons[0];
    const last = buttons[buttons.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }
</script>

<div class="backdrop">
  <div
    bind:this={box}
    class="sg-panel dialog"
    role="alertdialog"
    aria-modal="true"
    aria-labelledby="confirm-title"
    aria-describedby="confirm-message"
    tabindex="-1"
    onkeydown={onKey}
  >
    <h2 id="confirm-title">{title}</h2>
    <p id="confirm-message">{message}</p>
    <div class="actions">
      <button type="button" class="button" bind:this={cancelBtn} onclick={oncancel}>Cancel</button>
      <button type="button" class="button button-danger" onclick={onconfirm}>{confirmLabel}</button>
    </div>
  </div>
</div>
