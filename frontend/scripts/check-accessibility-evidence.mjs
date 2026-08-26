import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const reason = "candidate-run WCAG 2.2 AA audit evidence is required";

if (process.argv[1] === __filename) {
  console.error(`not_run: ${reason}`);
  process.exitCode = 3;
}
