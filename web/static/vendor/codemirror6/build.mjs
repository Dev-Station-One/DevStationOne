// Rebuild: cd web/static/vendor/codemirror6 && bun install && bun run build.mjs
// Output: cm6.bundle.js (committed; node_modules is not). Never edit
// cm6.bundle.js by hand — rerun this script.
import { execSync } from "node:child_process";
execSync("bunx esbuild entry.js --bundle --minify --format=iife --global-name=CM6 --target=es2018 --outfile=cm6.bundle.js", { stdio: "inherit" });
