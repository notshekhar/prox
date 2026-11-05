#!/usr/bin/env bun
/**
 * Copies the built dashboard to where the Go binary embeds it from.
 *
 * go:embed cannot reach outside its own package, so the bundle has to sit beside
 * the package that embeds it. That copy is committed: a clean checkout must build
 * with nothing but the Go toolchain. After changing the dashboard, run
 * `bun run build` here and commit both sides.
 */
import { copyFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';

const dashboard = join(import.meta.dir, '..');
const target = join(dashboard, '..', 'internal', 'proxcli', 'webdist', 'management.html');

mkdirSync(dirname(target), { recursive: true });
copyFileSync(join(dashboard, 'dist', 'index.html'), target);
