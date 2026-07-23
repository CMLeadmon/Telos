import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

export function verifyAccessibilityEvidence() {
  const auditPath = path.resolve(__dirname, "../e2e/accessibility-audit.md");
  if (!fs.existsSync(auditPath)) {
    throw new Error("accessibility-audit.md not found");
  }

  const content = fs.readFileSync(auditPath, "utf8");
  if (!content.includes("WCAG 2.2 AA") || !content.includes("PASS")) {
    throw new Error("accessibility-audit.md does not contain WCAG 2.2 AA certification");
  }

  return true;
}

if (process.argv[1] === __filename) {
  try {
    verifyAccessibilityEvidence();
    console.log("Accessibility evidence verified successfully.");
  } catch (err) {
    console.error("Accessibility evidence check failed:", err.message);
    process.exit(1);
  }
}
