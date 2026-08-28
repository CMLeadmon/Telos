# Gate Runner Containment Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every gate tool probe and command one unprivileged Linux process-ownership boundary that cleans complete descendant trees and finalizes truthful evidence after timeouts or catchable cancellation.

**Architecture:** Add a standard-library Linux supervisor that enables child-subreaper adoption, discovers task descendants through `/proc`, binds them to pidfds, and performs cooperative then forced shutdown without signaling numeric PIDs or PGIDs. Integrate that one primitive first with tool-version probes, then ordinary and nested gate execution, and finally runner signal/evidence flow.

**Tech Stack:** Python 3 standard library (`ctypes`, `dataclasses`, `os`, `selectors`, `signal`, `subprocess`), Linux `/proc`, `prctl`, pidfds, Bash integration fixtures, JSON gate catalogs.

**Spec:** `docs/superpowers/specs/2026-08-26-gate-runner-containment-design.md`

## Global Constraints

- Run only on Linux with `/proc`, `prctl`, `os.pidfd_open`, and `signal.pidfd_send_signal`; fail closed before creating the evidence directory when any primitive is unavailable.
- Use parent/child lineage under a process-wide child subreaper as ownership truth; do not serialize the inherited environment or add an environment ownership token.
- Open and verify pidfds for every owned live process. Numeric PIDs and PGIDs may be diagnostic values but are never destructive-signal targets.
- Execute one selected gate at a time. The supervisor may treat every new child adopted during the active launch as belonging to that launch only because execution remains sequential.
- Preserve argv-only execution with `shell=False`; never introduce `eval`, `bash -c`, or `sh -c` for catalog commands.
- `terminationGraceSeconds` is optional and non-negative. Its default is 1 second; `frontend-playwright` declares 25 seconds. Final escalation waits 1 second after TERM and at most 2 seconds after KILL.
- Tool-version probes use the same supervisor with a 5-second execution timeout and 1-second cooperative grace.
- SIGINT and SIGTERM finalize all selected gate evidence before returning 130 or 143. SIGKILL is explicitly outside that guarantee.
- Remove `sh` from truthful version support. Continue validating both `bash` and `sh` against interpreter-level `-c` command strings.
- Do not change the evidence-envelope schema, product architecture, Playwright port-ownership checks, dependencies, lockfiles, or applied migrations.
- Follow strict TDD: each production behavior begins with a real failing test, its RED output is retained in the task report, and only then is the minimum implementation added.
- Run every command from the repository root and keep test output free of unexpected warnings.

---

## File Structure

- Create: `scripts/lib/process_supervisor.py` — Linux support detection, child-subreaper activation, descendant discovery, stable pidfd identities, captured execution, cooperative shutdown, forced cleanup, and adopted-child reaping.
- Create: `tests/operations/process_supervisor_test.py` — focused behavioral contract for successful execution, session escape, double-fork adoption, leader exit, timeout, leak cleanup, and stable signaling.
- Create: `tests/fixtures/gates/supervised_process_tree.py` — harmless process-tree modes used only by the focused supervisor tests.
- Modify: `scripts/run-gates.py` — catalog grace validation, shell option parsing, supervised probes and commands, signal handling, cancellation evidence, and exit mapping.
- Modify: `tests/fixtures/gates/versioned_tool.py` — controlled probe timeout and leaked-descendant modes.
- Modify: `tests/fixtures/gates/ordinary_process_tree.py` — session-escaping, double-fork, cooperative-wrapper, and cancellation marker modes.
- Modify: `tests/operations/ci_gates_test.sh` — runner-level probe, command, nested Playwright, shell parsing, and cancellation regressions; invoke the focused supervisor contract.
- Modify: `ci/phase-gates.json` — declare the Playwright termination grace.
- Modify: `tests/operations/accumulated_suite_test.sh` — copy the runtime supervisor into its clean fixture repository.
- Modify: `scripts/verify-clean-checkout.sh` — inventory the new runtime-critical supervisor module.

---

### Task 1: Build the Linux process supervisor primitive

**Files:**
- Create: `scripts/lib/process_supervisor.py`
- Create: `tests/operations/process_supervisor_test.py`
- Create: `tests/fixtures/gates/supervised_process_tree.py`

**Interfaces:**
- Produces: `SignalState.receive(signum, frame)` and read-only `SignalState.signum: int | None`.
- Produces: `require_support() -> None` and `enable_child_subreaper() -> None`; both raise `SupervisionError` with a corrective Linux capability message.
- Produces: `run(argv, *, cwd, timeout_seconds, cooperative_grace_seconds, signals) -> SupervisedResult`.
- Produces: immutable `SupervisedResult(output: bytes, return_code: int | None, cause: str, cleanup_errors: tuple[str, ...], interrupted_signal: int | None)` with causes `exited`, `timeout`, `descendants`, `capture_error`, and `interrupted`.
- Consumes later: all process launches in `scripts/run-gates.py`.

- [ ] **Step 1: Write the failing supervisor contract**

Create `tests/operations/process_supervisor_test.py`. Before each test body, identify the production mutation it catches in a comment. Import the production module by putting `scripts/lib` on `sys.path`, enable the subreaper once, and exercise real child processes rather than mocked `Popen` objects.

The contract must include these literal assertions:

```python
success = supervisor.run(
    [sys.executable, "-c", "print('supervised-ok')"],
    cwd=repo_root,
    timeout_seconds=5,
    cooperative_grace_seconds=1,
    signals=supervisor.SignalState(),
)
self.assertEqual(success.cause, "exited")
self.assertEqual(success.return_code, 0)
self.assertEqual(success.output, b"supervised-ok\n")
self.assertEqual(success.cleanup_errors, ())
```

Run the fixture in `timeout`, `leader-exit`, `setsid`, and `double-fork`
modes. Each fixture writes every PID beneath a temporary state directory; the
test reads `/proc/<pid>/stat` and treats missing or `Z` as dead. Assert timeout
returns cause `timeout`, a zero-exit leader with surviving children returns
cause `descendants`, elapsed cleanup remains below 8 seconds, and every recorded
process is dead before the result is accepted.

Add a cancellation case whose `SignalState.receive(signal.SIGTERM, None)` is
called by a timer after the fixture writes `ready`. Assert cause `interrupted`,
`interrupted_signal == signal.SIGTERM`, no process survives, and the result
returns only after the fixture's cooperative cleanup marker exists.

- [ ] **Step 2: Run the contract and verify RED**

Run:

```bash
python3 -B tests/operations/process_supervisor_test.py
```

Expected: FAIL because `scripts/lib/process_supervisor.py` does not exist. Keep
the failing command, exception, and expected reason in the task report.

- [ ] **Step 3: Add the harmless process-tree fixture**

Implement `tests/fixtures/gates/supervised_process_tree.py` with these exact
modes and observable files:

```text
timeout       leader and stubborn child remain live
leader-exit   leader exits 0; stubborn child closes stdout and remains live
setsid        child calls os.setsid(), ignores TERM, and remains live
double-fork   grandchild calls os.setsid(), records grandchild.pid, and remains live
cooperative   leader records ready, forwards TERM to its child, waits, writes cleanup, and exits 143
```

Every live role writes `<role>.pid` atomically enough for the test to wait on
existence, installs only the signal behavior named by its mode, and redirects a
detached child's standard streams to `subprocess.DEVNULL` when the test needs to
distinguish a process leak from a capture-pipe leak.

- [ ] **Step 4: Implement support detection and stable identities**

Create `scripts/lib/process_supervisor.py` with no third-party imports. Use
`ctypes.CDLL(None, use_errno=True).prctl` for `PR_GET_CHILD_SUBREAPER` and
`PR_SET_CHILD_SUBREAPER`. `require_support()` checks Linux, readable `/proc`,
`os.pidfd_open`, and `signal.pidfd_send_signal`. `enable_child_subreaper()` sets
the flag and reads it back.

Represent each owned process with its numeric PID for `/proc` lookup, parsed
start time, and open pidfd. Open the pidfd between two start-time reads and
discard the identity unless both reads match. Parse `/proc/<pid>/stat` after the
final `)` so spaces and parentheses in the command name cannot shift fields.

- [ ] **Step 5: Implement discovery, capture, and bounded cleanup**

Implement `run` with this launch boundary:

```python
process = subprocess.Popen(
    argv,
    cwd=cwd,
    shell=False,
    start_new_session=True,
    stdout=subprocess.PIPE,
    stderr=subprocess.STDOUT,
)
```

Use nonblocking capture or
bounded `communicate` intervals so signal state and descendant discovery are
observed throughout execution.

Maintain the owned set from the leader lineage plus newly adopted direct
children created after launch. On timeout or interruption, signal the leader's
pidfd first and wait the requested cooperative grace. Then signal all remaining
owned pidfds with TERM, wait 1 second, signal survivors with KILL, and wait at
most 2 seconds. Continue discovering during every wait and reap only owned
children. Close every pidfd in a `finally` path. Return cleanup failures in
`cleanup_errors`; never call `os.kill`, `os.killpg`, `Popen.terminate`, or
`Popen.kill` for destructive cleanup.

- [ ] **Step 6: Run focused GREEN verification**

Run:

```bash
python3 -B tests/operations/process_supervisor_test.py
git diff --check
```

Expected: the focused contract passes with zero surviving fixture processes and
`git diff --check` prints nothing.

- [ ] **Step 7: Commit the supervisor primitive**

```bash
git add scripts/lib/process_supervisor.py \
  tests/operations/process_supervisor_test.py \
  tests/fixtures/gates/supervised_process_tree.py
git commit -m "test: add stable gate process supervision"
```

---

### Task 2: Supervise tool probes and correct catalog truth

**Files:**
- Modify: `scripts/run-gates.py`
- Modify: `tests/fixtures/gates/versioned_tool.py`
- Modify: `tests/operations/ci_gates_test.sh`
- Modify: `ci/phase-gates.json`
- Modify: `tests/operations/accumulated_suite_test.sh`
- Modify: `scripts/verify-clean-checkout.sh`

**Interfaces:**
- Consumes: `process_supervisor.require_support`, `enable_child_subreaper`, `SignalState`, and `run` from Task 1.
- Produces: optional catalog integer `terminationGraceSeconds >= 0`, defaulting through `gate.get("terminationGraceSeconds", 1)`.
- Produces: `shell_uses_command_string(command: list[str]) -> bool`.
- Produces: `probe_tool_versions(gate, signals=None) -> (dict[str, str], str | None, int | None)`, where the last value is the received interruption signal and an omitted state is a fresh unsignaled `SignalState` until Task 4 installs shared handlers.
- Preserves: existing evidence fields and existing `not_run` semantics for unavailable or unverifiable primary tools.

- [ ] **Step 1: Add failing catalog and probe regressions**

Extend `tests/operations/ci_gates_test.sh` to invoke the focused Task 1
contract, then add behavioral catalogs for:

```text
bash -eu -c 'printf unsafe'       rejected before evidence creation
bash -O extglob -c 'printf bad'   rejected before evidence creation
bash script.sh -c                 accepted as a script argv boundary
bash -- script.sh -c              accepted as a script argv boundary
terminationGraceSeconds = -1      rejected before evidence creation
terminationGraceSeconds = 1.0     rejected before evidence creation
terminationGraceSeconds = 0       accepted
```

For accepted `sh script.sh -c`, assert the catalog reaches gate evaluation and
writes a `not_run` envelope whose reason says tool identity is unsupported;
that proves validation accepted the argv while `sh` did not claim a false
version.

Extend `tests/fixtures/gates/versioned_tool.py` with `hang-with-child` and
`success-with-child` probe modes selected by `TELOS_VERSION_FIXTURE_MODE`. The
child calls `os.setsid`, ignores TERM, records its PID in
`TELOS_VERSION_FIXTURE_STATE`, and stays live. The success mode redirects the
child's streams to `DEVNULL`, prints the normal version, and exits zero.

Assert both modes return `not_run`, never create the gate-command marker, remain
bounded below 10 seconds, and leave every recorded probe process dead.

- [ ] **Step 2: Run the runner contract and verify RED**

Run:

```bash
bash tests/operations/ci_gates_test.sh
```

Expected: FAIL on the first new case because probes still use raw
`subprocess.run`, `sh` is still versioned, or script-level `-c` is rejected.
Record the first behaviorally relevant failure in the task report.

- [ ] **Step 3: Add optional grace validation and shell option parsing**

Separate required gate fields from optional allowed fields. Validate
`terminationGraceSeconds` only when present, using `is_integer` so booleans and
floats fail, and allow zero.

Implement interpreter option parsing with these option arguments:

```python
SHELL_OPTIONS_WITH_ARGUMENT = {
    "-O", "+O", "-o", "+o", "--init-file", "--rcfile",
}
```

Before the first script operand, reject any short option cluster whose option
letters contain `c`. Respect `--`, skip the following operand for the listed
two-argument options, and continue through long flags or attached option
values. Stop parsing at the first ordinary operand so `-c` passed to a script
is literal argv.

- [ ] **Step 4: Route version probes through the supervisor**

Remove `sh` from `VERSIONED_TOOLS`. Import `process_supervisor` from the existing
`scripts/lib` path. Make the five-second `[executable, "--version"]` launch use
`process_supervisor.run` with one-second cooperative grace and the active
`SignalState`.

Map supervisor causes exactly:

```text
timeout       tool version probe timed out for: <tool>
descendants   tool version probe left descendants running for: <tool>
capture_error tool version probe failed for <tool>: <cleanup detail>
interrupted   return the received signal to the caller without launching the gate
```

For `exited`, require return code zero and the first nonempty UTF-8 output line.
Decode with replacement for diagnostics and never record environment values.

- [ ] **Step 5: Activate support before evidence creation and update consumers**

In `main`, call `require_support()` and `enable_child_subreaper()` after catalog,
selection, candidate, and source validation but before
`create_evidence_directory`. Convert `SupervisionError` to `GateError` so an
unsupported platform exits 2 without creating evidence.

Set `"terminationGraceSeconds":25` only on `frontend-playwright` in
`ci/phase-gates.json`. Update its catalog assertion in
`tests/operations/ci_gates_test.sh`. Copy `scripts/lib/process_supervisor.py`
into both clean fixture repositories built by `ci_gates_test.sh` and
`accumulated_suite_test.sh`. Add `scripts/lib/process_supervisor.py` exactly once
to `required_paths()` in `scripts/verify-clean-checkout.sh`.

- [ ] **Step 6: Run focused GREEN verification**

Run:

```bash
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
bash scripts/verify-clean-checkout.sh --inventory-only
git diff --check
```

Expected: every command exits zero, probe descendants are gone, the Playwright
catalog grace is 25, and inventory reports every required input present.

- [ ] **Step 7: Commit supervised probe and catalog truth**

```bash
git add scripts/run-gates.py tests/fixtures/gates/versioned_tool.py \
  tests/operations/ci_gates_test.sh ci/phase-gates.json \
  tests/operations/accumulated_suite_test.sh scripts/verify-clean-checkout.sh
git commit -m "fix: contain gate tool probes"
```

---

### Task 3: Supervise ordinary gates and nested Playwright cleanup

**Files:**
- Modify: `scripts/run-gates.py`
- Modify: `tests/fixtures/gates/ordinary_process_tree.py`
- Modify: `tests/operations/ci_gates_test.sh`

**Interfaces:**
- Consumes: `process_supervisor.run` and `terminationGraceSeconds` from Tasks 1-2.
- Produces: `run_command(gate, signals=None) -> (status, reason, exit_code, output, interruption_signal)`; an omitted state is a fresh unsignaled `SignalState` until Task 4 passes the runner-wide state.
- Preserves: runner result codes 124 for timeout, 125 for leaked descendants, 126 for launch/capture failure, 3 for command-reported `not_run`, and normalized ordinary process exits.
- Preserves: `run-playwright-gate.py` as the nested owner of its npm/npx process sets.

- [ ] **Step 1: Add failing escaped-session and nested-supervisor tests**

Extend `tests/fixtures/gates/ordinary_process_tree.py` so its child can call
`os.setsid`, double-fork, close capture streams while remaining live, or record
a cooperative TERM marker. Keep existing `timeout` and `pipe-hang` modes.

Extend `tests/operations/ci_gates_test.sh` with real runner catalogs that prove:

```text
setsid-timeout     exit 124; separately sessioned stubborn child is dead
double-fork        exit 124 or 125 by fixture mode; grandchild is dead
leader-exit-leak   exit 125; log names descendants; detached child is dead
clean-zero         passed; exit 0; no false descendant result
```

Add a nested Playwright catalog that runs the existing wrapper through
`run-gates.py` with fixture npm/npx executables, an outer one-second timeout,
and `terminationGraceSeconds: 8`. Invoke the wrapper with
`--ready-timeout-seconds 5 --playwright-timeout-seconds 20
--shutdown-timeout-seconds 1` and use
`TELOS_GATE_FIXTURE_CHILD_MODE=cleanup-marker`.
Assert the outer runner exits nonzero with gate exit code 124, the inner
`playwright-cleanup-term.pid` marker exists before the runner returns, and all
nested server and Playwright PIDs are dead.

Add a forced-escalation nested fixture with zero cooperative grace. Assert it
returns within 8 seconds and leaves no live process.

- [ ] **Step 2: Run the runner lifecycle cases and verify RED**

Run:

```bash
bash tests/operations/ci_gates_test.sh
```

Expected: FAIL because the current process-group cleanup cannot see a child that
called `setsid`, and the one-second outer escalation can preempt nested cleanup.
Capture the relevant surviving PID or missing cleanup-marker failure.

- [ ] **Step 3: Replace process-group execution with the shared supervisor**

Delete `process_group_has_live_members`, `wait_for_process_group_exit`, and
`stop_process_group` from `scripts/run-gates.py`. Make `run_command` call:

```python
result = process_supervisor.run(
    gate["command"],
    cwd=REPO_ROOT,
    timeout_seconds=gate["timeoutSeconds"],
    cooperative_grace_seconds=gate.get("terminationGraceSeconds", 1),
    signals=signals,
)
```

Continue mapping `FileNotFoundError` to `not_run` and other launch `OSError`
values to failed exit 126. Map `SupervisedResult` causes to the preserved exit
codes and append cleanup errors to the captured log. For timeout, distinguish
leader-exited capture timeout from ordinary command timeout only when the
supervisor result exposes that fact; both remain exit 124. An `exited` result
with return code zero passes only after the supervisor has confirmed no owned
descendant remains.

- [ ] **Step 4: Preserve cooperative nested cleanup before escalation**

Pass the catalog grace unchanged to the supervisor. On timeout the supervisor
must signal only the wrapper leader during the cooperative interval; it must
not preemptively signal the wrapper's separately sessioned descendants. After
the interval, remaining descendants enter the shared TERM/KILL escalation.

The runner log must bind both the timeout line and any cleanup failure text.
The evidence `outputDigest` must equal SHA-256 of the final log bytes in every
new failure case.

- [ ] **Step 5: Run focused GREEN verification**

Run:

```bash
python3 -B tests/operations/process_supervisor_test.py
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
git diff --check
```

Expected: all commands exit zero; every `/proc` assertion confirms no fixture
process survives; the nested cleanup marker exists; evidence digests match log
bytes.

- [ ] **Step 6: Commit supervised gate execution**

```bash
git add scripts/run-gates.py tests/fixtures/gates/ordinary_process_tree.py \
  tests/operations/ci_gates_test.sh
git commit -m "fix: contain complete gate process trees"
```

---

### Task 4: Finalize evidence on SIGINT and SIGTERM

**Files:**
- Modify: `scripts/run-gates.py`
- Modify: `tests/fixtures/gates/versioned_tool.py`
- Modify: `tests/fixtures/gates/ordinary_process_tree.py`
- Modify: `tests/operations/ci_gates_test.sh`

**Interfaces:**
- Consumes: `SignalState` and interrupted supervisor results from Tasks 1-3.
- Produces: `execute_gates(gates, source_commit, candidate_digest, evidence_dir, signals) -> (passed: bool, interrupted_signal: int | None)`.
- Produces: active failed evidence reason `runner interrupted by SIGINT during tool version probe`, `runner interrupted by SIGTERM during tool version probe`, `runner interrupted by SIGINT during gate execution`, or `runner interrupted by SIGTERM during gate execution`.
- Produces: later-gate `not_run` reason `runner interrupted before execution`.
- Produces: process exit 130 for SIGINT and 143 for SIGTERM after all selected evidence is durable.

- [ ] **Step 1: Add failing cancellation evidence tests**

Add runner-level background-process helpers to
`tests/operations/ci_gates_test.sh`. Each helper creates a two-gate catalog:
the first gate blocks in a controlled probe or command and the second gate is
independent and would write a marker if executed. Wait for the first fixture's
ready marker, signal the `run-gates.py` PID, wait for exit, then inspect real
logs, envelopes, and `/proc`.

Cover this matrix:

```text
SIGINT   during tool version probe   runner 130; active failed 130; later not_run 3
SIGTERM  during tool version probe   runner 143; active failed 143; later not_run 3
SIGINT   during gate execution       runner 130; active failed 130; later not_run 3
SIGTERM  during gate execution       runner 143; active failed 143; later not_run 3
```

For every row assert the active reason names the signal and stage, the later
gate marker does not exist, `outputDigest` matches the exact log bytes, the
evidence verifier accepts both envelopes, and every fixture PID is missing or
zombie before the runner has exited.

Add a cooperative command fixture whose cleanup marker is written only after
the forwarded signal is received. Assert that marker exists before final
evidence is read. Add a second-signal case and assert the first signal continues
to determine the exit code while cleanup still completes.

- [ ] **Step 2: Run cancellation cases and verify RED**

Run:

```bash
bash tests/operations/ci_gates_test.sh
```

Expected: FAIL because SIGINT or SIGTERM currently terminates the runner before
the active envelope is finalized and can leave the fixture descendant live.
Retain one probe-stage and one command-stage failure in the report.

- [ ] **Step 3: Install minimal runner signal handlers**

Create one `process_supervisor.SignalState` in `main`. Install its `receive`
method for SIGINT and SIGTERM immediately before `execute_gates`, restore the
previous handlers in `finally`, and never raise or perform I/O inside the signal
handler. Pass that same state through `execute_gates`, `probe_tool_versions`,
and `run_command`.

If a signal already exists before a gate begins, do not launch its probe or
command. Treat only the first catchable signal as authoritative.

- [ ] **Step 4: Write active and remaining cancellation evidence**

When a supervised probe or command returns `interrupted`, finish the active
gate as `failed` using exit `128 + signum`, captured output plus one UTF-8
interruption line, and the exact stage-specific reason from this task's
interface. Do not overwrite that reason with a later source-check result.

For every remaining selected gate, write a log containing exactly:

```text
not_run: runner interrupted before execution
```

Use its normal command, subject kind, source commit, candidate digest, Python
tool version, and exit code 3 when creating the envelope. Do not probe tools,
evaluate prerequisites, or execute commands after cancellation.

Return `(False, signum)` from `execute_gates`; `main` returns `128 + signum`
only after all writes finish. If no signal occurred, preserve the existing
boolean pass result and exits 0/1.

- [ ] **Step 5: Run the complete remediation verification**

Run:

```bash
python3 -B tests/operations/process_supervisor_test.py
bash tests/operations/evidence_envelope_test.sh
bash tests/operations/beta_certification_test.sh
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
bash tests/operations/product_truth_test.sh
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
git diff --check
git status --short
```

Expected: every executable gate exits zero, negative fixture cases are handled
inside their suites, `git diff --check` prints nothing, and `git status` shows
only the intended Task 4 files before commit.

- [ ] **Step 6: Commit cancellation-safe evidence**

```bash
git add scripts/run-gates.py tests/fixtures/gates/versioned_tool.py \
  tests/fixtures/gates/ordinary_process_tree.py \
  tests/operations/ci_gates_test.sh
git commit -m "fix: finalize gate evidence on cancellation"
```

---

## Cycle Exit Gate

After all four task reviews are clean, run the Phase 1 exit suite from the
approved parent plan plus the focused supervisor contract:

```bash
python3 -B tests/operations/process_supervisor_test.py
bash tests/operations/evidence_envelope_test.sh
bash tests/operations/beta_certification_test.sh
bash tests/operations/ci_gates_test.sh
bash tests/operations/accumulated_suite_test.sh
bash tests/operations/product_truth_test.sh
bash scripts/check-product-truth.sh
bash scripts/check-release-truth.sh
bash scripts/verify-clean-checkout.sh --inventory-only
npm --prefix frontend run lint
(cd frontend && npx tsc --noEmit)
npm --prefix frontend run test:unit
npm --prefix frontend run build
```

Then request one whole-branch code review against the cycle base. The reviewer
must explicitly assess tool probes, new-session and double-fork descendants,
nested Playwright cooperation, stable pidfd signaling, cancellation evidence,
shell parsing, and the clean-checkout fixture copies. Any final-review findings
receive one consolidated fix wave and one scoped re-review as required by the
Subagent-Driven Development workflow.
