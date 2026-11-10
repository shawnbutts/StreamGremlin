// The theme side of a wheel look's messages
// (docs/adr/0036-packages-plugins-and-themes.md). The overlay page that
// frames this one sends what to show; this page only answers that it's
// ready, that an animation finished, or that it couldn't show a spin.
// Messages from anything but the framing page are ignored.

export const PROTOCOL = 1;

/**
 * Listens for the overlay page's messages and calls handlers[type] with
 * the payload. Returns send(type, payload).
 */
export function bridge(win, handlers) {
  const parent = win.parent;
  win.addEventListener('message', (ev) => {
    if (ev.source !== parent || parent === win) return;
    const msg = ev.data;
    if (!msg || typeof msg !== 'object' || msg.protocolVersion !== PROTOCOL || typeof msg.type !== 'string') return;
    const fn = Object.prototype.hasOwnProperty.call(handlers, msg.type) ? handlers[msg.type] : null;
    if (fn) fn(msg.payload && typeof msg.payload === 'object' ? msg.payload : {});
  });
  // The framing page's origin can't be named from a sandboxed page (its
  // own origin is opaque), and nothing sent here is private.
  return (type, payload) => parent.postMessage({ protocolVersion: PROTOCOL, type, payload: payload || {} }, '*');
}

/** Whether OBS's page API reached this frame, for the live check. */
export function obsApi(win) {
  try {
    return typeof win.obsstudio === 'object' && win.obsstudio !== null;
  } catch {
    return false;
  }
}
