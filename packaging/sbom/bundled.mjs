// Lists the npm packages a bundle holds, for the release SBOMs (D-117).
// The desktop UI's Vite build and the Stream Deck plugin's esbuild build
// each pass the files they bundled; `release sbom` reads the list. Only
// packages whose code or data is in the bundle are listed, not the
// tools that built it. Plain Node, no packages of its own.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';

const marker = '/node_modules/';

/**
 * The packages the given module files belong to, by name and version,
 * with the licence their package.json declares.
 * @param {Iterable<string>} ids module ids or paths from the bundler
 * @returns {{name: string, version: string, license: string}[]}
 */
export function packagesOf(ids) {
  const byDir = new Map();
  for (const raw of ids) {
    const id = resolve(raw.replace(/^\0/, '').split('?')[0]).replaceAll('\\', '/');
    const at = id.lastIndexOf(marker);
    if (at < 0) continue;
    const parts = id.slice(at + marker.length).split('/');
    const name = parts[0].startsWith('@') ? `${parts[0]}/${parts[1]}` : parts[0];
    const dir = id.slice(0, at + marker.length) + name;
    if (byDir.has(dir)) continue;
    const pkg = JSON.parse(readFileSync(`${dir}/package.json`, 'utf8'));
    byDir.set(dir, { name: pkg.name, version: pkg.version, license: typeof pkg.license === 'string' ? pkg.license : '' });
  }
  const unique = new Map();
  for (const p of byDir.values()) unique.set(`${p.name}@${p.version}`, p);
  return [...unique.values()].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : a.version < b.version ? -1 : 1));
}

/**
 * Writes the list, with anything bundled that isn't an npm package.
 * @param {string} file where, usually bin/sbom/NAME.json
 * @param {{name: string, version: string, license: string}[]} packages
 * @param {{name: string, version: string, license: string, purl?: string}[]} [vendored]
 */
export function writePackages(file, packages, vendored = []) {
  mkdirSync(dirname(file), { recursive: true });
  writeFileSync(file, `${JSON.stringify({ packages, vendored }, null, 2)}\n`);
}
