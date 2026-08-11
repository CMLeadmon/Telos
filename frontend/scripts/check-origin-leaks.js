/* eslint-disable @typescript-eslint/no-require-imports */
const fs = require("fs");
const path = require("path");

const srcDir = path.join(__dirname, "..", "src");
let violations = 0;

function walk(dir, fileList = []) {
  const files = fs.readdirSync(dir);
  for (const file of files) {
    const filePath = path.join(dir, file);
    const stat = fs.statSync(filePath);
    if (stat.isDirectory()) {
      walk(filePath, fileList);
    } else if (filePath.endsWith(".ts") || filePath.endsWith(".tsx")) {
      fileList.push(filePath);
    }
  }
  return fileList;
}

const allFiles = walk(srcDir);

// Rule 1: No raw fetch() or new XMLHttpRequest() outside lib/api.ts and lib/upload.ts
for (const file of allFiles) {
  const relPath = path.relative(srcDir, file);
  if (
    relPath === "lib/api.ts" ||
    relPath === "lib/upload.ts" ||
    file.endsWith(".test.ts") ||
    file.endsWith(".test.tsx")
  ) {
    continue;
  }
  const content = fs.readFileSync(file, "utf8");
  if (/\bfetch\(/.test(content)) {
    console.error(`Violation in ${relPath}: raw fetch() call detected.`);
    violations++;
  }
  if (/new\s+XMLHttpRequest\(/.test(content)) {
    console.error(`Violation in ${relPath}: raw XMLHttpRequest detected.`);
    violations++;
  }
}

// Rule 2: no src/poster expression may reach the gateway without assetUrl().
//
// Matching on the whole brace-balanced expression rather than a line, and on
// the property rather than one variable name. The first version of this rule
// tested for the literal `src={item.coverUrl}` on a single line, which caught
// exactly one of the shapes in the tree: `detail.coverUrl` in
// StreamItemDetail.tsx and any attribute wrapped across lines both passed it
// clean. A gate that reports green while the leak it exists to catch is present
// is worse than no gate.
function boundExpressions(content) {
  const found = [];
  const marker = /\b(?:src|poster)=\{/g;
  let match;
  while ((match = marker.exec(content)) !== null) {
    const start = match.index + match[0].length;
    let depth = 1;
    let i = start;
    while (i < content.length && depth > 0) {
      if (content[i] === "{") depth++;
      else if (content[i] === "}") depth--;
      i++;
    }
    found.push({ expr: content.slice(start, i - 1), index: match.index });
  }
  return found;
}

// The helpers that make a URL origin-aware. assetUrl() is the general one;
// the rest already prepend apiBase() internally, so an expression built from
// any of them is absolutized and must not be flagged.
const ORIGIN_AWARE =
  /\b(?:assetUrl|apiBase|avatarUrl|libraryCoverUrl|libraryContentUrl)\s*\(/;

function originAware(expr, content) {
  if (ORIGIN_AWARE.test(expr)) return true;
  // One level of indirection, for src={coverSrc} where coverSrc was built with
  // a helper a few lines above. Anything deeper is not worth guessing at from
  // a regex, and would be better served by a real type-aware rule.
  const identifier = expr.trim().match(/^[A-Za-z_$][\w$]*$/);
  if (!identifier) return false;
  const declaration = content.match(
    new RegExp(`\\b(?:const|let|var)\\s+${identifier[0]}\\b[^;]*;`),
  );
  return Boolean(declaration && ORIGIN_AWARE.test(declaration[0]));
}

for (const file of allFiles) {
  if (!file.endsWith(".tsx") || file.endsWith(".test.tsx")) continue;
  const relPath = path.relative(srcDir, file);
  const content = fs.readFileSync(file, "utf8");

  for (const { expr, index } of boundExpressions(content)) {
    const reachesGateway = /cover/i.test(expr) || expr.includes("/api/v1/");
    if (!reachesGateway || originAware(expr, content)) continue;
    const line = content.slice(0, index).split("\n").length;
    console.error(
      `Violation in ${relPath}:${line}: bound to the gateway without assetUrl(): ${expr.trim().replace(/\s+/g, " ")}`,
    );
    violations++;
  }
}

if (violations > 0) {
  console.error(`Found ${violations} origin leak violation(s). Build aborted.`);
  process.exit(1);
} else {
  console.log("Origin leak check passed cleanly.");
  process.exit(0);
}
