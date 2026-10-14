#!/usr/bin/env python3
"""Contract for .github/workflows/ci.yml.

Every job and step is stated here independently of the workflow so that a
silently removed check, a skipped step, a tolerated failure, a missing tool
setup or a bare Playwright invocation changes this test's verdict. The
truth-and-gates job additionally keeps its stricter line-level validation in
ci_gates_test.sh.
"""
import copy
import shutil
import subprocess
import sys
import unittest
from pathlib import Path

try:
    import yaml
except ImportError as error:  # a missing prerequisite is a failure, never a skip
    raise SystemExit(f"ci workflow contract prerequisite missing: PyYAML ({error})")

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github/workflows/ci.yml"

NODE = {"uses": "actions/setup-node@v4", "with": {"node-version": "24.18.0"}}
GO = {"uses": "actions/setup-go@v5", "with": {"go-version": "1.26.5"}}
CHECKOUT = {"uses": "actions/checkout@v4"}


def run(*lines):
    return {"run": list(lines)}


EXPECTED_JOBS = {
    "backend-gates": {
        "steps": [
            CHECKOUT, GO,
            run("cd backend", "go test -race -v -coverprofile=coverage.out ./..."),
            run("cd backend", "go vet ./..."),
        ],
    },
    "backend-integration": {
        "min_timeout_minutes": 30,
        "steps": [
            CHECKOUT,
            run("podman --version", "podman info --format '{{.Host.OCIRuntime.Name}}'"),
            run("bash scripts/test-backend.sh"),
        ],
    },
    "compose-validation": {
        "steps": [
            CHECKOUT,
            run("sudo apt-get update",
                "sudo apt-get install -y podman podman-compose python3-yaml"),
            run("podman --version", "podman-compose --version",
                "python3 -c 'import yaml'"),
            run("bash scripts/validate-compose.sh --env-file .env.example"),
        ],
    },
    "frontend-gates": {
        "steps": [
            CHECKOUT, NODE,
            run("npm --prefix frontend ci"),
            run("npm --prefix frontend run lint"),
            run("node frontend/node_modules/typescript/bin/tsc "
                "--project frontend/tsconfig.json --noEmit"),
            run("bash tests/operations/frontend_types_gate_test.sh"),
            run("npm --prefix frontend run test:unit"),
            run("npm --prefix frontend run build"),
            run("npm --prefix frontend run check:bundle"),
        ],
    },
    "frontend-playwright": {
        "min_timeout_minutes": 40,
        "steps": [
            CHECKOUT, NODE,
            run("npm --prefix frontend ci"),
            run("npm --prefix frontend exec --no -- playwright install "
                "--with-deps chromium webkit"),
            run("python3 scripts/run-playwright-gate.py --playwright-timeout-seconds 1650"),
        ],
    },
    "static-client": {
        "min_timeout_minutes": 20,
        "steps": [
            CHECKOUT, NODE,
            run("npm --prefix frontend ci", "npm --prefix frontend run build"),
            run("sudo apt-get update", "sudo apt-get install -y nginx-core"),
            {"run": ["bash tests/operations/static_client_config_test.sh"], "env": {"TELOS_NGINX": "/usr/sbin/nginx"}},
            run("bash tests/operations/static_client_test.sh"),
        ],
    },
    "browser-node-fixtures": {
        "min_timeout_minutes": 15,
        "steps": [
            CHECKOUT,
            run("sudo apt-get update", "sudo apt-get install -y nginx-core"),
            {"run": ["bash tests/operations/browser_node_fixtures_test.sh"], "env": {"TELOS_NGINX": "/usr/sbin/nginx"}},
        ],
    },
    "check-headless-build": {
        "steps": [
            CHECKOUT, GO,
            run("rm -rf frontend", "cd backend", "go test ./...",
                "go build -o /tmp/telos-core-headless ."),
        ],
    },
    "check-api-contract-drift": {
        "steps": [
            CHECKOUT, GO,
            run("cd backend", "go run ./cmd/genopenapi --check"),
        ],
    },
    "check-cross-compile": {
        "matrix": {"goos": ["linux"], "goarch": ["amd64", "arm64"]},
        "steps": [
            CHECKOUT, GO,
            {"run": ["cd backend", "go build -o /dev/null ./cmd/telos"],
             "env": {"GOOS": "${{ matrix.goos }}", "GOARCH": "${{ matrix.goarch }}"}},
        ],
    },
    "operations-fixtures": {
        "min_timeout_minutes": 30,
        "steps": [
            CHECKOUT, NODE,
            run("sudo apt-get update", "sudo apt-get install -y ripgrep python3-yaml postgresql-16 podman"),
            run("bash tests/operations/image_lock_test.sh", "bash tests/operations/runtime_hardening_test.sh"),
            run("bash tests/operations/monitoring_test.sh"),
            run("bash tests/operations/backup_fixtures_test.sh"),
            {"run": ["bash tests/operations/restore_fixtures_test.sh"], "env": {"TELOS_PG_BIN": "/usr/lib/postgresql/16/bin"}},
            run("bash tests/operations/upgrade_rollback_fixtures_test.sh"),
            run("bash tests/operations/capacity_exposure_test.sh"),
            run("bash tests/operations/supply_chain_test.sh"),
        ],
    },
    "supply-chain-source": {
        "min_timeout_minutes": 20,
        "steps": [
            {"uses": "actions/checkout@v4", "with": {"fetch-depth": 0}}, GO, NODE,
            run("python3 scripts/lib/supply_chain.py policy"),
            run("bash scripts/verify-release-pins.sh"),
            run('GITLEAKS="$(python3 scripts/bootstrap-ci-tools.py --tool gitleaks --cache-dir "$RUNNER_TEMP/ci-tools")"',
                '"$GITLEAKS" git . --no-banner --exit-code 1'),
            run("cd backend", "go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./..."),
            run("npm --prefix frontend ci", "npm --prefix frontend audit --omit=dev --audit-level=moderate"),
        ],
    },
    "truth-and-gates": {
        "steps": [
            CHECKOUT, NODE,
            run("sudo apt-get install -y ripgrep python3-yaml"),
            run("bash tests/operations/evidence_envelope_test.sh"),
            run("bash tests/operations/beta_certification_test.sh"),
            run("bash tests/operations/ci_gates_test.sh"),
            run("bash tests/operations/accumulated_suite_test.sh"),
            run("python3 tests/operations/platform_scope_test.py"),
            run("bash tests/operations/release_test.sh"),
            run("bash tests/operations/product_truth_test.sh"),
            run("bash scripts/check-product-truth.sh"),
            run("bash scripts/check-release-truth.sh"),
            run("bash scripts/verify-clean-checkout.sh --inventory-only"),
        ],
    },
}

STEP_KEYS = {"name", "uses", "with", "run", "env"}
JOB_KEYS = {"runs-on", "steps", "timeout-minutes", "strategy", "permissions"}


class ContractError(ValueError):
    pass


def normalise(step):
    value = {key: step[key] for key in step if key != "name"}
    if "run" in value:
        value["run"] = [line.strip() for line in str(value["run"]).splitlines() if line.strip()]
    return value


def validate(document):
    if document.get("permissions") != {"contents": "read"}:
        raise ContractError("workflow permissions must be exactly contents: read")
    if "defaults" in document:
        raise ContractError("workflow must not define defaults")
    jobs = document.get("jobs")
    if not isinstance(jobs, dict):
        raise ContractError("workflow has no jobs mapping")
    if set(jobs) != set(EXPECTED_JOBS):
        raise ContractError(
            f"job set differs from the contract: missing={sorted(set(EXPECTED_JOBS) - set(jobs))} "
            f"unexpected={sorted(set(jobs) - set(EXPECTED_JOBS))}")
    for name, expected in EXPECTED_JOBS.items():
        job = jobs[name]
        if "if" in job:
            raise ContractError(f"{name} job is conditional")
        if "continue-on-error" in job:
            raise ContractError(f"{name} job permits continuation after failure")
        unknown = set(job) - JOB_KEYS
        if unknown:
            raise ContractError(f"{name} has unknown direct key: {sorted(unknown)}")
        if job.get("runs-on") != "ubuntu-latest":
            raise ContractError(f"{name} must run on ubuntu-latest")
        timeout = job.get("timeout-minutes")
        minimum = expected.get("min_timeout_minutes")
        if minimum is not None and (not isinstance(timeout, int) or timeout < minimum):
            raise ContractError(f"{name} timeout-minutes must be at least {minimum}")
        if "matrix" in expected:
            strategy = job.get("strategy", {})
            if strategy.get("matrix") != expected["matrix"] or strategy.get("fail-fast") is not False:
                raise ContractError(f"{name} matrix differs from the contract")
        steps = job.get("steps")
        if not isinstance(steps, list):
            raise ContractError(f"{name} has no steps sequence")
        for step in steps:
            unknown = set(step) - STEP_KEYS
            if "if" in step:
                raise ContractError(f"{name} step is conditional")
            if "continue-on-error" in step:
                raise ContractError(f"{name} step permits continuation after failure")
            if "shell" in step or "defaults" in step or "working-directory" in step:
                raise ContractError(f"{name} step overrides shell or working directory")
            if unknown:
                raise ContractError(f"{name} step has unknown direct key: {sorted(unknown)}")
        actual = [normalise(step) for step in steps]
        if actual != expected["steps"]:
            raise ContractError(
                f"{name} steps differ from the contract (a removed, reordered or altered step "
                f"also changes the tool setup that precedes its dependents)")


class WorkflowContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.document = yaml.safe_load(WORKFLOW.read_text(encoding="utf-8"))

    def mutated(self):
        return copy.deepcopy(self.document)

    def assertRejected(self, document, fragment):
        with self.assertRaises(ContractError) as caught:
            validate(document)
        self.assertIn(fragment, str(caught.exception))

    def test_repository_workflow_satisfies_contract(self):
        validate(self.document)

    def test_every_step_removal_is_rejected(self):
        for name, job in self.document["jobs"].items():
            for index in range(len(job["steps"])):
                document = self.mutated()
                del document["jobs"][name]["steps"][index]
                self.assertRejected(document, "steps differ")

    def test_every_job_removal_is_rejected(self):
        for name in self.document["jobs"]:
            document = self.mutated()
            del document["jobs"][name]
            self.assertRejected(document, "job set differs")

    def test_unexpected_job_is_rejected(self):
        document = self.mutated()
        document["jobs"]["extra"] = copy.deepcopy(document["jobs"]["backend-gates"])
        self.assertRejected(document, "job set differs")

    def test_skipping_any_step_is_rejected(self):
        for name, job in self.document["jobs"].items():
            for index in range(len(job["steps"])):
                document = self.mutated()
                document["jobs"][name]["steps"][index]["if"] = "${{ false }}"
                self.assertRejected(document, "step is conditional")

    def test_tolerated_step_failure_is_rejected(self):
        for name, job in self.document["jobs"].items():
            for index in range(len(job["steps"])):
                for value in (True, "${{ true }}", False):
                    document = self.mutated()
                    document["jobs"][name]["steps"][index]["continue-on-error"] = value
                    self.assertRejected(document, "permits continuation")

    def test_job_conditions_and_tolerated_failure_are_rejected(self):
        for name in self.document["jobs"]:
            document = self.mutated()
            document["jobs"][name]["if"] = "${{ false }}"
            self.assertRejected(document, "job is conditional")
            document = self.mutated()
            document["jobs"][name]["continue-on-error"] = True
            self.assertRejected(document, "job permits continuation")

    def test_shell_and_defaults_overrides_are_rejected(self):
        document = self.mutated()
        document["jobs"]["truth-and-gates"]["steps"][3]["shell"] = "/bin/true {0}"
        self.assertRejected(document, "overrides shell")
        document = self.mutated()
        document["defaults"] = {"run": {"shell": "/bin/true {0}"}}
        self.assertRejected(document, "must not define defaults")

    def test_node_setup_must_precede_dependents_and_pin_the_repository_version(self):
        for name in ("frontend-gates", "frontend-playwright", "truth-and-gates"):
            steps = self.document["jobs"][name]["steps"]
            node_index = next(i for i, step in enumerate(steps) if step.get("uses") == NODE["uses"])
            document = self.mutated()
            moved = document["jobs"][name]["steps"]
            moved.append(moved.pop(node_index))
            self.assertRejected(document, "steps differ")
            document = self.mutated()
            document["jobs"][name]["steps"][node_index]["with"]["node-version"] = "22"
            self.assertRejected(document, "steps differ")
        nvmrc = (ROOT / ".nvmrc").read_text(encoding="utf-8").strip()
        self.assertEqual(NODE["with"]["node-version"], nvmrc)

    def test_playwright_must_use_the_managed_server_wrapper(self):
        job = "frontend-playwright"
        index = len(self.document["jobs"][job]["steps"]) - 1
        for command in (
            "npx playwright test",
            "npm --prefix frontend exec -- playwright test",
            "python3 scripts/run-playwright-gate.py",
        ):
            document = self.mutated()
            document["jobs"][job]["steps"][index]["run"] = command
            self.assertRejected(document, "steps differ")

    def test_playwright_job_budget_covers_the_catalog_gate(self):
        document = self.mutated()
        document["jobs"]["frontend-playwright"]["timeout-minutes"] = 20
        self.assertRejected(document, "timeout-minutes")
        document = self.mutated()
        del document["jobs"]["frontend-playwright"]["timeout-minutes"]
        self.assertRejected(document, "timeout-minutes")

    def test_integration_job_needs_a_bounded_budget(self):
        document = self.mutated()
        del document["jobs"]["backend-integration"]["timeout-minutes"]
        self.assertRejected(document, "timeout-minutes")

    def test_compose_validation_requires_provider_and_pyyaml_before_rendering(self):
        steps = self.document["jobs"]["compose-validation"]["steps"]
        install = next(i for i, s in enumerate(steps) if "apt-get install" in s.get("run", ""))
        render = next(i for i, s in enumerate(steps) if "validate-compose.sh" in s.get("run", ""))
        self.assertLess(install, render)
        self.assertIn("python3-yaml", steps[install]["run"])
        self.assertIn("podman-compose", steps[install]["run"])
        document = self.mutated()
        document["jobs"]["compose-validation"]["steps"][render]["run"] = (
            "bash scripts/validate-compose.sh --env-file .env")
        self.assertRejected(document, "steps differ")

    def test_accessibility_evidence_checker_stays_fail_closed(self):
        # `npm run check:a11y-evidence` requires candidate-run WCAG audit evidence
        # that no CI job can produce, so the checker exits 3 (not_run) and the
        # workflow deliberately omits it: a permanently red step is not a gate.
        # This keeps the stub honest until a real evidence validator replaces it.
        node = shutil.which("node")
        self.assertIsNotNone(node, "node is a required prerequisite for this contract")
        completed = subprocess.run(
            [node, "frontend/scripts/check-accessibility-evidence.mjs"],
            cwd=ROOT, text=True, capture_output=True, check=False)
        self.assertEqual(completed.returncode, 3, completed.stdout + completed.stderr)
        self.assertIn("not_run", completed.stderr)
        for name, job in self.document["jobs"].items():
            for step in job["steps"]:
                self.assertNotIn("check:a11y-evidence", str(step.get("run", "")), name)


if __name__ == "__main__":
    unittest.main(argv=sys.argv)
