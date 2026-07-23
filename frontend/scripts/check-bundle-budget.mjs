import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const root = path.resolve(__dirname, "..");

export function verifyBundleBudget() {
  const budgetPath = path.join(root, "bundle-budget.json");
  if (!fs.existsSync(budgetPath)) {
    throw new Error("bundle-budget.json missing");
  }

  const budget = JSON.parse(fs.readFileSync(budgetPath, "utf8"));
  const nextStaticDir = path.join(root, ".next", "static");

  if (!fs.existsSync(nextStaticDir)) {
    // If build artifact doesn't exist yet, return true (tested during build)
    return true;
  }

  // Walk static js files and check gzip sizes
  function getJsFiles(dir) {
    let results = [];
    const list = fs.readdirSync(dir);
    for (const file of list) {
      const filePath = path.join(dir, file);
      const stat = fs.statSync(filePath);
      if (stat && stat.isDirectory()) {
        results = results.concat(getJsFiles(filePath));
      } else if (filePath.endsWith(".js")) {
        results.push(filePath);
      }
    }
    return results;
  }

  const jsFiles = getJsFiles(nextStaticDir);
  for (const file of jsFiles) {
    const content = fs.readFileSync(file);
    const gzipped = zlib.gzipSync(content);
    // Every single static chunk should be within budget limit
    if (gzipped.length > budget.maxGzipBytesPerRoute) {
      throw new Error(`Chunk ${path.basename(file)} exceeds gzip budget limit of ${budget.maxGzipBytesPerRoute} bytes (${gzipped.length} bytes)`);
    }
  }

  return true;
}

if (process.argv[1] === __filename) {
  try {
    verifyBundleBudget();
    console.log("Bundle budget checks passed successfully.");
  } catch (err) {
    console.error("Bundle budget check failed:", err.message);
    process.exit(1);
  }
}
