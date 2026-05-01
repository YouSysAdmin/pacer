// SPDX-License-Identifier: LicenseRef-DSL-1.0
// Deferred Source License (DSL)
// Pacer, Copyright (c) 2026 YouSysAdmin
//
// Post-build HTML minifier. Vite already minifies the JS and CSS
// bundles via esbuild; adapter-static's prerendered .html files come
// out pretty-printed and are not touched. This walks dist/ and
// rewrites every .html in place with a conservative minifier config.
//
// Conservative on purpose: SvelteKit's prerendered shell contains
// inline bootstrap script + __sveltekit placeholders that the runtime
// rewrites. minifyJS / minifyCSS are off so we don't risk breaking
// either; the size win comes from whitespace + comment removal.

import { promises as fs } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { minify } from "html-minifier-terser";

const opts = {
  collapseWhitespace: true,
  conservativeCollapse: true,
  removeComments: true,
  removeRedundantAttributes: true,
  removeScriptTypeAttributes: true,
  removeStyleLinkTypeAttributes: true,
  useShortDoctype: true,
  decodeEntities: false,
  caseSensitive: true,
};

async function walk(dir, out = []) {
  const entries = await fs.readdir(dir, { withFileTypes: true });
  for (const e of entries) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) await walk(p, out);
    else if (e.name.endsWith(".html")) out.push(p);
  }
  return out;
}

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "..", "dist");

try {
  await fs.access(root);
} catch {
  console.error(`dist/ not found at ${root} -- run 'vite build' first`);
  process.exit(1);
}

const files = await walk(root);
let savedBytes = 0;

for (const f of files) {
  const before = await fs.readFile(f, "utf8");
  const after = await minify(before, opts);
  if (after.length < before.length) {
    await fs.writeFile(f, after);
    savedBytes += before.length - after.length;
  }
}

const kb = (savedBytes / 1024).toFixed(1);
console.log(`minified ${files.length} html files, saved ${kb} KB`);
