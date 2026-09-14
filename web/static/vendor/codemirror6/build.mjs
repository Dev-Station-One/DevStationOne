// Rebuild: cd web/static/vendor/codemirror6 && bun install && bun run build.mjs
// Output: cm6.bundle.js + chunks/*.js (all committed; node_modules is not).
// Never edit them by hand — rerun this script. ESM+splitting (not IIFE) so
// @codemirror/language-data's per-language dynamic import()s lazy-load as
// separate chunk files instead of bundling all ~180 languages eagerly.
import { execSync } from "node:child_process";
import { rmSync } from "node:fs";

// Chunk filenames are content-hashed; esbuild doesn't clean stale ones from
// a previous build, so old hashes pile up as orphaned committed files.
rmSync("chunks", { recursive: true, force: true });
execSync("bunx esbuild entry.js --bundle --minify --format=esm --splitting --outdir=. --entry-names=cm6.bundle --chunk-names=chunks/[name]-[hash] --target=es2018", { stdio: "inherit" });
