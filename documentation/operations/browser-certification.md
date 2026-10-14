# Telos Supported Browser and Responsive Certification Guide

Browser coverage remains cross-platform despite the Ubuntu-only server policy: Windows and Mac browsers, desktop
Safari and mobile browsers stay in scope ([platform scope](../product/platform-scope.md)). A Playwright WebKit run is
useful automation but never replaces real Safari or real-device evidence.

> **Status.** The tooling is implemented and tested with ephemeral keys: the guided recorder, the Playwright report
> tool, the signed-record aggregation behind the `browser-matrix` and `desktop-linux` gates, and candidate-mode
> Playwright projects. **No record exists yet.** No tester is enrolled in `release/evidence-signers`, no physical
> iOS/Android device or macOS Safari has been used, and the automated journeys cover 9 of the 13 listed (below). Until
> real records exist both gates report `not_run`, and a rehearsal never satisfies them.

## The seven browser entries

| Platform id | How | Question list |
|---|---|---|
| `chrome`, `edge`, `firefox`, `webkit` | automated: Playwright against the deployed candidate URL | `automatedAssertions` (13 journeys) |
| `safari-desktop`, `safari-ios`, `chrome-android` | guided: a person on the real browser/device | `assertions` (15 questions) |

`release/guided-assertions.json` is the one list of questions. A record answers every question of its platform's list,
in that order, with `pass`, `fail` or `not_run`; nothing can be added, dropped or reordered. WebKit automation is its own
platform and cannot stand in for either Safari; a desktop browser cannot stand in for Android Chrome; emulation (an
`automated` device model) cannot stand in for a guided platform, and the reverse. Linux desktop is the same machinery
with its own matrix (`desktop`, platform `linux-appimage`, 11 questions from `desktop-beta.md`).

## Automated platforms

```bash
cd frontend
TELOS_CANDIDATE_BROWSERS=1 E2E_BASE_URL=https://node.example E2E_REQUIRE_LIVE=1 \
  E2E_OWNER_USERNAME=… E2E_OWNER_PASSWORD=… E2E_MEMBER_USERNAME=… E2E_MEMBER_PASSWORD=… \
  PLAYWRIGHT_JSON_OUTPUT_NAME=/secure/dir/chrome.json npx playwright test --project=chrome
```

Candidate mode runs only `e2e/hosted-node.spec.ts` in the four automated projects, with trace, video and screenshots
off (the journeys carry credentials), no retries and no mocked API. Journeys are tied to questions by an
`@assertion:<id>` tag in the test title. Then:

```bash
python3 scripts/lib/browser_report.py --platform chrome --playwright-json chrome.json --candidate-lock candidate-lock.json \
  --artifact images/telos-client.oci.tar --browser-version "Google Chrome 141.0.7390.54" --started … --finished … --out records/chrome.json
```

A question with no test is `not_run`; a failing, timed-out, interrupted or flaky test is `fail`; the raw report is never
attached (it can hold URLs and headers), only a derived per-test summary. Today nine questions have tests
(`trusted-https`, `member-auth`, `chat`, `files`, `commentary`, `stream-library-states`, `settings-admin`,
`logout-login`, and the axe-core audit `accessibility-axe`); `owner-and-invite`, `upstream-degradation`, `offline-reconnect` and `responsive-layout` have no
candidate-bound test yet, so the automated records answer them `not_run` and the gate stays `not_run` — by design, not by omission.

## Guided platforms

```bash
python3 scripts/record-guided-smoke.py --matrix browser --platform safari-ios \
  --candidate-lock candidate-lock.json --artifact images/telos-client.oci.tar --out records/safari-ios.json
```

The recorder proves the artifact is the candidate's exact hosted-client image, shows one numbered question at a time,
accepts only `pass`, `fail` or `not_run` with a bounded note (credential-like text becomes `[redacted]`), hashes the
attachments you list and writes canonical bytes. It cannot write an overall verdict. It signs with
`TELOS_EVIDENCE_SIGNING_KEY` for `TELOS_EVIDENCE_SIGNING_PRINCIPAL` (namespace `telos-guided-smoke-v1`); an enrolled
tester's signature is required. With no enrolled tester or key it saves an **unsigned draft outside the records
directory** and exits 3. Enrol testers with `scripts/enroll-release-signer.py --purpose evidence`.

## Aggregation

```bash
bash scripts/certify-browser-matrix.sh --candidate-lock candidate-lock.json --records-dir records --result-out result.json
bash scripts/certify-desktop.sh        --candidate-lock candidate-lock.json --records-dir desktop-records --result-out result.json
```

The records directory holds exactly `<platform>.json`, `<platform>.json.sig` and `<platform>.attachments/`. The aggregator
verifies each signature against `release/evidence-signers`, re-validates the record (candidate and artifact digests,
catalog order, times not future or reversed, freshness, hashed attachments, no secrets, no byte-identical record reused
for two platforms) and derives `passed` only when every answer of every platform is `pass`. A missing platform or any
`not_run` answer makes the gate `not_run` (exit 3); a `fail`, bad signature or invalid record makes it `failed`. The
result is a producer result whose subject is the shared external-host descriptor; the signed records and attachments are
its attachments.
