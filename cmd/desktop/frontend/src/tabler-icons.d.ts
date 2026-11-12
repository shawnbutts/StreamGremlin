// The Tabler Icons outline pack, aliased in vite.config.ts (the package
// exports only its single SVG files).
declare module 'tabler-icons-outline' {
  const pack: Record<string, [string, Record<string, string>][]>;
  export default pack;
}
