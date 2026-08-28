# Gate Runner Containment Remediation — Design

**Date:** 2026-08-26

**Status:** Review pending

## Objective

Close the remaining Phase 1 process-lifecycle gaps in the standalone beta gate
runner without requiring privileged host configuration. Every catalog tool probe
and gate command must run inside one Linux-owned process boundary that survives
forks, new sessions, leader exit, and ordinary daemonization. A timeout or a
catchable interruption must leave no owned process running and must still
publish truthful final evidence.

This remediation keeps the existing standalone client/server architecture and
gate catalog. It changes the gate runner's execution boundary, not Telos product
behavior, deployment topology, or the evidence-envelope schema.

## Current-State Findings

The Phase 1 runner already provides an argv-only catalog, bounded commands,
per-gate logs, and evidence envelopes. Three lifecycle gaps still block the
phase from being treated as merge-ready:

- Tool identity uses a raw `subprocess.run(..., timeout=5)` call
  (`scripts/run-gates.py:354-381`). A timed-out executable can leave descendants
  behind because Python terminates only the process it launched.
- Ordinary gate cleanup owns a numeric process group and gives it a fixed
  one-second TERM grace (`scripts/run-gates.py:384-457`). Descendants can create
  new sessions, and a numeric process-group ID can be reused before a later
  destructive signal.
- `run_command` does not handle SIGINT or SIGTERM
  (`scripts/run-gates.py:460-531`). An interrupted runner can bypass gate
  cleanup and exit before writing the active gate's final log and envelope.

The outer timeout also conflicts with the nested Playwright supervisor. The
outer runner currently escalates after one second
(`scripts/run-gates.py:427-447`), while the Playwright wrapper can spend five
seconds in each of several owned-process cleanup stages
(`scripts/run-playwright-gate.py:508-519`,
`scripts/run-playwright-gate.py:586-603`). Killing the wrapper before its
cooperative cleanup completes can strand its separately sessioned npm or npx
descendants (`scripts/run-playwright-gate.py:522-536`).

Two smaller truth defects belong in the same cycle. `sh` is listed as a
versioned tool even though a portable `sh --version` identity does not exist
(`scripts/run-gates.py:35`, `scripts/run-gates.py:354-381`), and shell-fragment
validation searches every later argument for `-c`
(`scripts/run-gates.py:124-133`). The latter incorrectly rejects a literal
script argument such as `bash script.sh -c`.

## Approaches Considered

### 1. Linux subreaper plus pidfds — selected

Make the gate runner a child subreaper, discover the active command's complete
descendant lineage through `/proc`, and bind every destructive signal to a
pidfd. Orphans are reparented to the runner even after double-forking or
`setsid`, and pidfds cannot be redirected by PID or process-group reuse. This
requires no root access and is available on the Ubuntu 24.04 beta reference
host.

### 2. Cgroup v2 or transient systemd scopes

A task-owned cgroup is the strongest kernel boundary and makes enumeration
simple. It is not selected because writable delegated cgroups and a usable
systemd manager are not guaranteed in rootless Podman containers, developer
shells, or hosted CI runners. Falling back silently when those facilities are
missing would make the same gate mean different things on different hosts.

### 3. Extend process groups and environment ownership tokens

This would be the smallest patch, but a child can leave its process group by
starting a session. Token discovery can also fail after credential or dumpable
state changes, and a later signal to a bare numeric process group retains a
reuse race. It does not meet the lifecycle contract.

## Locked Design Decisions

| Decision | Remediation choice |
|---|---|
| Supported runner platform | Linux with `/proc`, `prctl`, `pidfd_open`, and `pidfd_send_signal` |
| Ownership source | Parent/child lineage under a process-wide child subreaper |
| Stable process identity | pidfds opened and verified against `/proc` start time |
| Privileged dependency | None; no cgroup or systemd requirement |
| Gate parallelism | Unchanged: one selected gate executes at a time |
| Cooperative shutdown | Per-gate catalog grace, with a short default and a longer Playwright value |
| Final escalation | Signal remaining owned pidfds, wait a bounded interval, then SIGKILL and reap |
| Catchable cancellation | Finalize active and remaining evidence, then exit `128 + signal` |
| `sh` tool identity | Unsupported; remove it from the versioned-tool set |
| Existing Playwright wrapper | Retained as the inner service/test supervisor |

## Process Supervisor Boundary

Add a focused library under `scripts/lib/` that owns Linux process lifecycle.
It has three responsibilities:

1. enable and verify `PR_SET_CHILD_SUBREAPER` before any catalog command runs;
2. launch one argv-only process in a new session and bind its leader to a pidfd;
3. discover, signal, and reap every process descended from that launch.

The supervisor does not use a secret-bearing serialized environment or a shell.
The command inherits the runner environment through `Popen` exactly as it does
today. Ownership comes from kernel parentage. While a task is active, the
supervisor repeatedly reads `/proc/<pid>/stat`, builds the parent graph, and
adds descendants of the leader or already owned processes. A process that
double-forks, starts a new session, or loses its original parent is adopted by
the subreaper and remains part of the one active task. The runner's existing
sequential execution is therefore a load-bearing invariant.

Every discovered live process is opened through `pidfd_open`; its start time is
checked before and after the open. Destructive signals use only the resulting
pidfd through `pidfd_send_signal`. Numeric PIDs and PGIDs may appear in
diagnostics, but they are never destructive-signal targets. The supervisor
closes all pidfds and reaps adopted children before returning a result.

The runner fails closed before creating the evidence directory if required
Linux ownership primitives are unavailable. Candidate gates must not degrade
to process-group cleanup or claim equivalent evidence on a weaker platform.

## Execution and Shutdown Flow

Tool-version probes and gate commands use the same supervisor API. A result
contains captured output, the normalized exit code, and one terminal cause:
ordinary exit, timeout, descendant leak, launch failure, capture failure, or
external interruption.

For an ordinary successful leader exit, the supervisor finishes draining the
capture pipe and confirms that no owned live descendants remain. A command that
exits but leaves a descendant is failed, the descendant set is terminated, and
the log identifies the lifecycle violation.

On timeout or external interruption, shutdown proceeds in two levels:

1. Send SIGTERM, or the received SIGINT/SIGTERM, to the stable leader identity
   only. Wait the gate's declared cooperative grace while continuing to track
   descendants. This lets `run-playwright-gate.py` receive the signal and clean
   its npm and npx ownership sets itself.
2. If any owned process remains, signal every remaining pidfd with SIGTERM,
   wait one second, then SIGKILL any survivors and wait up to two more seconds.
   Continue discovery during both waits so a late fork cannot escape, and reap
   all adopted children before returning.

The gate catalog gains an optional non-negative `terminationGraceSeconds`
field. Absence means a one-second cooperative grace. The Playwright catalog
entry declares 25 seconds: its two owned groups can each consume a five-second
TERM wait and a five-second KILL wait, with five seconds of scheduling margin.
No runner code infers policy from a command name or parses nested wrapper
arguments. This field controls cleanup after the execution deadline and does
not extend the gate's declared work timeout. Tool-version probes use the
one-second cooperative grace.

A version probe that times out, fails, or leaves descendants produces the
existing fail-closed `not_run` gate result and never launches the gate command.
Its owned processes are cleaned before evidence is written.

## Cancellation and Evidence

`run-gates.py` installs minimal SIGINT and SIGTERM handlers that record only the
first received signal. The active supervisor observes that state outside the
signal handler and performs bounded cleanup. The active gate then receives a
final failed log and envelope with exit code 130 or 143 and a reason identifying
whether interruption occurred during tool identity or command execution.

After the active envelope is durable, every selected gate not yet started is
written as `not_run` with reason `runner interrupted before execution`.
Prerequisite status remains recorded, but cancellation takes precedence over
running any otherwise independent later gate. The process exits with
`128 + signal` only after all selected gate evidence has been finalized.

A second catchable signal does not bypass cleanup. SIGKILL remains inherently
uncatchable and cannot promise final evidence; that operating-system boundary
is stated explicitly rather than disguised as a supported path.

## Catalog and Shell Truth

Remove `sh` from `VERSIONED_TOOLS`. A catalog can still name an arbitrary
executable, but a gate whose primary executable has no supported truthful
identity remains `not_run`, consistent with the existing fail-closed behavior
for unknown tools (`scripts/run-gates.py:354-381`). Bash, Python, Node, npm,
npx, Podman, and Git keep explicit version probes.

Replace the current all-arguments shell scan with interpreter-option parsing.
For `bash` and `sh`, reject `-c` or a short-option cluster containing `c` only
before the script/operand boundary. Respect `--` and option arguments that
precede that boundary. Thus `bash -eu -c '...'` is rejected, while
`bash script.sh -c` and `bash -- script.sh -c` are accepted as argv-only script
invocations.

## Testing Strategy

Extend the harmless fixtures under `tests/fixtures/gates/` and the gate-runner
contract suite in `tests/operations/ci_gates_test.sh`.

Required tests prove:

- a hanging tool probe with a stubborn child is bounded and leaves no live
  fixture process;
- a nominally successful tool probe that leaves a separately sessioned child
  becomes `not_run`, and the gate command never executes;
- a timed-out gate cannot escape through `setsid`, double-forking, leader exit,
  or a capture-pipe holder;
- a nested Playwright fixture receives cooperative SIGTERM through
  `run-gates.py`, writes its cleanup marker, and leaves neither its npm nor npx
  tree alive;
- forced escalation kills a nested supervisor that does not finish inside its
  declared grace;
- SIGINT and SIGTERM during both version probing and command execution produce
  exit 130/143, a failed active envelope, `not_run` envelopes for later gates,
  matching output digests, and no surviving descendants;
- a gate that exits zero with no descendants still passes;
- shell validation rejects interpreter `-c` forms but accepts `-c` after the
  script boundary; and
- `sh` no longer claims a version identity.

The cycle retains the existing evidence, beta-certification, accumulated-suite,
product-truth, release-truth, clean-checkout, frontend, and build gates. Tests
must inspect `/proc` process state rather than relying only on PID-file absence.

## Scope Boundaries

This cycle does not introduce parallel gate execution, cgroup management,
Windows or macOS gate-runner support, a new evidence schema, or changes to
product services. It does not replace the Playwright wrapper's port ownership
or inner npm/npx lifecycle checks. The new outer boundary provides
candidate-run defense in depth for those nested processes, while direct
standalone wrapper execution retains its previously documented trusted
repo-owned command model.

## Acceptance Criteria

The remediation is complete when:

1. probes and gate commands share the Linux subreaper/pidfd supervisor;
2. timeout, leader exit, new sessions, nested cleanup, and catchable signals
   leave no owned process alive;
3. cancellation emits final evidence for every selected gate before returning
   130 or 143;
4. no destructive cleanup signal uses a bare numeric PID or PGID;
5. shell validation and `sh` identity behavior match this specification;
6. the exact Phase 1 exit suite and the new lifecycle regressions pass from a
   clean checkout; and
7. a fresh whole-branch code review finds no load-bearing process-lifecycle
   defect in the Phase 1 gate runner.
