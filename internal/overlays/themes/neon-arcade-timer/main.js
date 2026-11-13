// The Neon arcade look for clocks and timers (D-092): the wheel look of the same
// name's materials, around the time. All it does is in theme.css.
import { timerTheme } from '../shared/timertheme.js';

if (typeof window !== 'undefined' && !window.__streamgremlinThemeTest) timerTheme(window);
