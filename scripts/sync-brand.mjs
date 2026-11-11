#!/usr/bin/env node
// The brand kit is the source of truth for both web surfaces.
import { readFile, writeFile, mkdir, readdir } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const check = process.argv.includes('--check');
const mappings = [];
const add = (source, target) => mappings.push([source, target]);

for (const surface of ['web', 'website']) {
  for (const file of await readdir(path.join(root, 'brand/svg'))) {
    if (file.endsWith('.svg')) add(`brand/svg/${file}`, `${surface}/public/brand/${file}`);
  }
  for (const file of await readdir(path.join(root, 'brand/favicon'))) {
    add(`brand/favicon/${file}`, `${surface}/public/${file}`);
  }
  for (const file of await readdir(path.join(root, 'brand/fonts'))) {
    add(`brand/fonts/${file}`, `${surface}/public/fonts/${file}`);
  }
  add('brand/patterns/dotshift-offset-pattern.svg', `${surface}/public/brand/dotshift-offset-pattern.svg`);
  add('brand/tokens/brand.css', `${surface}/${surface === 'web' ? 'src' : 'app'}/brand-tokens.css`);
}
add('brand/social/dotshift-social-card.png', 'website/public/dotshift-social-card.png');
let changed = 0;
for (const [source, target] of mappings) {
  const expected = await readFile(path.join(root, source));
  const actual = await readFile(path.join(root, target)).catch(error => {
    if (error.code !== 'ENOENT') throw error;
    return null;
  });
  if (actual?.equals(expected)) continue;
  changed++;
  if (check) {
    console.error(`Outdated brand asset: ${target}`);
  } else {
    await mkdir(path.dirname(path.join(root, target)), {recursive: true});
    await writeFile(path.join(root, target), expected);
  }
}
if (check && changed) process.exitCode = 1;
else console.log(check ? `Verified ${mappings.length} brand assets.` : `Synchronized ${changed} of ${mappings.length} brand assets.`);
