// The Neon arcade look: glowing wedges on a dark cabinet.
import { el } from '../shared/wheel.js';
import { wheelTheme } from '../shared/wheeltheme.js';

export const style = {
  palette: ['#0b8fa3', '#6a2fc7', '#1f9e4c', '#b8287a'],
  edge: '#c9fbff',
  labelStroke: '#04121c',
  rim(svg, doc, cx) {
    svg.appendChild(el(doc, 'circle', { cx, cy: cx, r: 246, fill: '#060d1c', stroke: 'var(--accent)', 'stroke-width': 6, class: 'glow' }));
    svg.appendChild(el(doc, 'circle', { cx, cy: cx, r: 218, fill: '#000000', stroke: '#9b5cff', 'stroke-width': 3 }));
  },
};

if (typeof window !== 'undefined' && !window.__streamgremlinThemeTest) wheelTheme(window, style);
