import fs from 'fs';

const content = fs.readFileSync('scripts/en_orig.json', 'utf-8');
const data = JSON.parse(content);
console.log('parsed keys:', Object.keys(data.translation).length);

const lines = content.split('\n');
const keyRe = /^\s{4}"((?:[^"\\]|\\.)*)":/;
const seen = new Map();
for (const line of lines) {
  const m = line.match(keyRe);
  if (m) {
    const k = m[1];
    seen.set(k, (seen.get(k) || 0) + 1);
  }
}
let totalRaw = 0;
let dupKeys = 0;
let dupLines = 0;
for (const [k, c] of seen) {
  totalRaw += c;
  if (c > 1) {
    dupKeys++;
    dupLines += c - 1;
  }
}
console.log('raw key lines:', totalRaw, 'unique raw keys:', seen.size, 'dup keys:', dupKeys, 'extra dup lines:', dupLines);
