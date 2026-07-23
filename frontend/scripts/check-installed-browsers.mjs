import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const root = path.resolve(__dirname, "..");

export function verifyInstalledBrowsers() {
  const certPath = path.join(root, "browser-certification.json");
  if (!fs.existsSync(certPath)) {
    throw new Error("browser-certification.json not found");
  }

  const cert = JSON.parse(fs.readFileSync(certPath, "utf8"));
  if (!cert.supportedProjects || cert.supportedProjects.length === 0) {
    throw new Error("No supported projects defined in browser-certification.json");
  }

  return true;
}

if (process.argv[1] === __filename) {
  try {
    verifyInstalledBrowsers();
    console.log("Installed browsers verification passed.");
  } catch (err) {
    console.error("Installed browsers verification failed:", err.message);
    process.exit(1);
  }
}
