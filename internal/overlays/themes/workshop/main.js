// The Workshop look: a brass and wood frame, parchment and copper wedges.
import { el } from '../shared/wheel.js';
import { wheelTheme } from '../shared/wheeltheme.js';

export const style = {
  palette: ['#f2deb0', '#b06d36', '#dcb25f'],
  edge: '#5c3416',
  labelStroke: '',
  rim(svg, doc, cx) {
    const defs = el(doc, 'defs');
    const g = el(doc, 'radialGradient', { id: 'brass', cx: '38%', cy: '35%', r: '75%' });
    for (const [offset, colour] of [['0%', '#fde9a9'], ['45%', '#c48d3f'], ['80%', '#6e451c'], ['100%', '#e7bd72']]) {
      g.appendChild(el(doc, 'stop', { offset, 'stop-color': colour }));
    }
    defs.appendChild(g);
    svg.appendChild(defs);
    svg.appendChild(el(doc, 'circle', { cx, cy: cx, r: 246, fill: 'url(#brass)', stroke: '#4a2b10', 'stroke-width': 6 }));
    svg.appendChild(el(doc, 'circle', { cx, cy: cx, r: 218, fill: '#2a1a0c' }));
  },
};

if (typeof window !== 'undefined' && !window.__streamgremlinThemeTest) wheelTheme(window, style);
