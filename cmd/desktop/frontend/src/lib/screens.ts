// The app's screens, in sidebar order (docs/adr/0019-app-comfort.md). App
// builds the sidebar from these; a Help test checks each has an entry.

export type Screen =
  | 'home'
  | 'live'
  | 'connections'
  | 'events'
  | 'workflows'
  | 'timers'
  | 'quotes'
  | 'sounds'
  | 'overlays'
  | 'giveaways'
  | 'points'
  | 'viewers'
  | 'filters'
  | 'wheels'
  | 'rewards'
  | 'polls'
  | 'variables'
  | 'addons'
  | 'help'
  | 'settings';

// The main screens in this order, then "More…" with the ones the streamer
// moved there, then Settings and Help, always last. Home, Live, Events,
// Workflows, Settings and Help never move. Ctrl/⌘+1–9 go to the main screens
// in order; Settings is Ctrl/⌘+comma and Help Ctrl/⌘+/, as in most apps.
export const screens: { id: Screen; label: string }[] = [
  { id: 'home', label: 'Home' },
  { id: 'live', label: 'Live' },
  { id: 'connections', label: 'Connections' },
  { id: 'events', label: 'Events' },
  { id: 'workflows', label: 'Workflows' },
  { id: 'timers', label: 'Timers' },
  { id: 'quotes', label: 'Quotes' },
  { id: 'sounds', label: 'Sounds' },
  { id: 'overlays', label: 'Overlays' },
  { id: 'giveaways', label: 'Giveaways' },
  { id: 'points', label: 'Points' },
  { id: 'viewers', label: 'Viewers' },
  { id: 'filters', label: 'Chat filters' },
  { id: 'wheels', label: 'Wheels' },
  { id: 'rewards', label: 'Rewards' },
  { id: 'polls', label: 'Polls' },
  { id: 'variables', label: 'Variables' },
  { id: 'addons', label: 'Add-ons' },
];

export const pinned: { id: Screen; label: string; key: string }[] = [
  { id: 'settings', label: 'Settings', key: ',' },
  { id: 'help', label: 'Help', key: '/' },
];
