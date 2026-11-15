export interface BundledPackage {
  name: string;
  version: string;
  license: string;
  purl?: string;
}
export function packagesOf(ids: Iterable<string>): BundledPackage[];
export function writePackages(file: string, packages: BundledPackage[], vendored?: BundledPackage[]): void;
