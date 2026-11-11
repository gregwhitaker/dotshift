import {createRequire} from 'node:module';
import {fileURLToPath} from 'node:url';
import path from 'node:path';
import fs from 'node:fs/promises';
const require = createRequire(import.meta.url);
const brand = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
let sharp;
try { sharp = require('sharp'); }
catch { sharp = require(require.resolve('sharp', {paths: [path.resolve(brand, '../website')]})); }
const jobs = JSON.parse(await fs.readFile(path.join(brand, 'source/raster-exports.json'), 'utf8'));
for (const job of jobs) {
  const svg = await fs.readFile(path.join(brand, job.source));
  await fs.mkdir(path.dirname(path.join(brand, job.target)), {recursive: true});
  await sharp(svg, {density: 384}).resize(job.width, job.height).png().toFile(path.join(brand, job.target));
}
// ICO directory with PNG-compressed frames, preserving alpha at each size.
const frames = await Promise.all([16, 32, 48].map(n => fs.readFile(path.join(brand, `favicon/favicon-${n}.png`))));
const header = Buffer.alloc(6 + 16 * frames.length);
header.writeUInt16LE(1, 2); header.writeUInt16LE(frames.length, 4);
let offset = header.length;
for (let i=0; i<frames.length; i++) {
  const start = 6 + i*16; const size = [16,32,48][i];
  header[start] = size; header[start+1] = size;
  header.writeUInt16LE(1, start+4); header.writeUInt16LE(32, start+6);
  header.writeUInt32LE(frames[i].length, start+8); header.writeUInt32LE(offset, start+12);
  offset += frames[i].length;
}
await fs.writeFile(path.join(brand, 'favicon/favicon.ico'), Buffer.concat([header, ...frames]));
console.log(`Rendered ${jobs.length} PNGs and the favicon ICO.`);
