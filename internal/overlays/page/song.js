// The Now playing overlay (D-091): the song Pretzel's desktop app is
// playing, from the files it writes, in the overlay's look. It shows only
// what the engine sends: a song while one plays, nothing otherwise. Song
// names are only ever set with textContent, and the cover only ever comes
// through the overlay's own link.

// How long leaving takes, as in overlay.js.
const OUT_MS = { slide: 450, pop: 450, unfold: 750 };

// A sample song shows this long, then the real one comes back.
export const SAMPLE_MS = 6000;

// The cover, through the overlay's own link; it changes name when it changes.
const ART = /^art\?v=[0-9a-f-]{1,40}$/;

const same = (a, b) => Boolean(a && b && a.title === b.title && a.artist === b.artist && (a.picture || '') === (b.picture || ''));

/**
 * Builds the Now playing display in stage. opts: setTimeout, and look(),
 * animation() and pace() for the overlay's current look.
 */
export function songView(doc, stage, opts = {}) {
  const later = opts.setTimeout || ((fn, ms) => setTimeout(fn, ms));
  const look = opts.look || (() => ({}));
  const animation = opts.animation || (() => 'slide');
  const pace = opts.pace || (() => 1);

  let song = null; // the song on screen, if any
  let real = null; // the engine's latest song, under a sample
  let box = null;
  let turn = 0; // each change's number: a timer from an older one does nothing

  function build(v) {
    const el = doc.createElement('div');
    el.className = 'sg-alert sg-song';
    // Bars that bounce while it plays, where a picture would go; under
    // the cover, when there is one, in case it won't load.
    const icon = doc.createElement('div');
    icon.className = 'sg-avatar sg-song-icon';
    icon.setAttribute('aria-hidden', 'true');
    for (let i = 0; i < 3; i++) {
      const bar = doc.createElement('span');
      bar.className = 'sg-eq';
      icon.appendChild(bar);
    }
    if (typeof v.picture === 'string' && ART.test(v.picture)) {
      const img = doc.createElement('img');
      img.className = 'sg-picture sg-song-art';
      img.alt = '';
      img.addEventListener('error', () => img.replaceWith(icon));
      img.src = v.picture;
      el.appendChild(img);
    } else {
      el.appendChild(icon);
    }
    const words = doc.createElement('div');
    words.className = 'sg-words';
    const label = doc.createElement('div');
    label.className = 'sg-title';
    label.textContent = 'Now playing';
    words.appendChild(label);
    const title = doc.createElement('div');
    title.className = 'sg-text sg-song-title';
    title.textContent = String(v.title || '');
    words.appendChild(title);
    if (v.artist) {
      const artist = doc.createElement('div');
      artist.className = 'sg-song-artist';
      artist.textContent = String(v.artist);
      words.appendChild(artist);
    }
    el.appendChild(words);
    return el;
  }

  function leave(el) {
    if (!el) return;
    el.classList.remove('in');
    el.classList.add('out');
    later(() => el.remove(), Math.round((OUT_MS[animation()] || 450) * pace()));
  }

  function show(v) {
    const n = ++turn;
    const old = box;
    box = null;
    leave(old);
    const enter = () => {
      if (n !== turn) return;
      box = build(v);
      stage.appendChild(box);
      void box.offsetWidth; // draw the starting state first
      box.classList.add('in');
      const l = look() || {};
      if (l.song_hides) {
        const ms = Math.max(2, Math.min(60, Number(l.seconds) || 6)) * 1000;
        later(() => {
          if (n === turn && box) {
            leave(box);
            box = null;
          }
        }, ms);
      }
    };
    // The old song leaves before the new one comes in.
    if (old) later(enter, Math.round((OUT_MS[animation()] || 450) * pace()));
    else enter();
  }

  const view = {
    /** The engine's song: { playing, title, artist, picture, sample }. */
    song(v) {
      if (!v || !v.playing || !v.title) {
        if (v && v.sample) return;
        song = real = null;
        turn++;
        leave(box);
        box = null;
        return;
      }
      if (!v.sample) {
        real = v;
        if (same(v, song) && box) return;
      }
      song = v;
      show(v);
      if (v.sample) {
        // A sample shows for a moment, then the real song comes back.
        const n = turn;
        later(() => {
          if (n !== turn) return;
          if (real) {
            song = real;
            show(real);
          } else view.song(null);
        }, SAMPLE_MS);
      }
    },
    /** The look changed: show the song again in it. */
    relook() {
      if (song) show(song);
    },
  };
  return view;
}
