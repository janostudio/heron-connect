#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');

const command = process.argv[2];

if (command === 'next') {
  // Usage: node local-version.js next ./npm/package.json <installed-package.json>
  // Prints the next local version string (increment patch, add -local suffix).
  const sourcePath = process.argv[3];
  const installedPath = process.argv[4];

  const source = JSON.parse(fs.readFileSync(sourcePath, 'utf8'));
  let base = source.version || '0.0.0';

  // If installed package exists, try to increment from its version
  if (installedPath && fs.existsSync(installedPath)) {
    try {
      const installed = JSON.parse(fs.readFileSync(installedPath, 'utf8'));
      const installedVer = installed.version || '';
      // If it ends with -local.N, increment N
      const m = installedVer.match(/^(.+)-local\.(\d+)$/);
      if (m && m[1] === base) {
        base = m[1] + '-local.' + (parseInt(m[2], 10) + 1);
        console.log(base);
        process.exit(0);
      }
    } catch (_) {}
  }

  console.log(base + '-local.1');
  process.exit(0);
}

if (command === 'write-package') {
  // Usage: node local-version.js write-package ./npm/package.json <installed-package.json> <version>
  const sourcePath = process.argv[3];
  const installedPath = process.argv[4];
  const version = process.argv[5];

  const source = JSON.parse(fs.readFileSync(sourcePath, 'utf8'));
  source.version = version;

  fs.writeFileSync(installedPath, JSON.stringify(source, null, 2) + '\n');
  process.exit(0);
}

console.error('Usage: node local-version.js <next|write-package> ...');
process.exit(1);
