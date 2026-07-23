# Telos Beta Phase 6: Frontend Quality Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every shipped Telos route responsive, permission-correct, recoverable, accessible to WCAG 2.2 AA, performance-budgeted, and testable across the approved browser/device matrix.

**Architecture:** Drive UI visibility from server capabilities, give mobile layouts explicit navigation and scroll ownership, model transport failure separately from anonymous auth, share one bounded reconnect/reconcile transport, consolidate accessible interaction primitives, and load heavy voice/media/reader dependencies only after feature entry.

**Tech Stack:** Next.js 16.2.10, React 19, TypeScript, Zustand 5, Vitest 4.1.10, Testing Library, `@axe-core/playwright` 4.12.1, Playwright 1.61.1, Next build manifests

## Global Constraints

- Execution mode is Fable 5 Medium inline: execute one task per run and stop after its focused commit.
- Begin only after the complete Phase 5 product gate is accepted.
- Preserve visual identity, theme tokens, canonical logos, and content hierarchy; this is a quality pass, not an unrelated redesign.
- Server authorization remains authoritative. Capability-driven visibility never replaces backend enforcement.
- No visible control is inert, mouse-only, hover-only, or inaccessible by keyboard.
- Optimistic mutations keep a persisted snapshot and user draft until acknowledgement.
- Auth transport failure never becomes anonymous unless the server returns authoritative `401`.
- Chat, user-event, and Watch Party sockets share one reconnect algorithm and reconcile durable state after every reconnection.
- Chat and notification reconciliation pages their Phase 5 forward sequence APIs through one sampled high-water mark before applying later socket hints.
- Authenticated initial JavaScript is at most 256,000 gzip bytes per ordinary shell route; LiveKit, HLS.js, PDF.js, and EPUB.js are absent from routes that have not activated them.

## Entry Gate

- [ ] Phase 5 evidence identifies the exact starting commit and migration checksum set through `0018`.
- [ ] Product-truth, backend product, frontend unit/build, and Chromium Phase 5 suites pass.
- [ ] Capture baseline screenshots at 390×844, 412×915, 768×1024, and 1440×900 plus route bundle sizes.
- [ ] Record current axe, keyboard, focus, 200% zoom, and reduced-motion failures as the before-state.

## File Responsibility Map

| Area | Files |
|---|---|
| Capabilities/admin | `frontend/src/lib/capabilities.ts`, quality pass over the working Phase 5 Settings forms, Stream/Library management surfaces |
| Mobile/responsive | `frontend/src/components/MobileNavigation.tsx`, AppShell/VoiceDock, module CSS, mobile layout E2E |
| Failure recovery | auth/preferences/chat/notification/Watch stores, `ConnectivityBanner`, `reconnectingSocket.ts` |
| Accessibility | `Dialog.tsx`, `useDialogFocus.ts`, `GlobalSearch.tsx`, interactive feature components, accessibility E2E |
| Performance | bundle budget/config/scripts, dynamic feature boundaries, narrow store selectors |
| Browser matrix | `frontend/playwright.config.ts`, shared fixtures, stateful product suite |

---

## P6-T1: Harden Administration and Modules Around Effective Capabilities

**Closes:** P08, F02

**Files:**

- Create: `frontend/src/lib/capabilities.ts`
- Create: `frontend/src/lib/capabilities.test.ts`
- Create: `frontend/e2e/capabilities.spec.ts`
- Modify: `frontend/src/app/(shell)/settings/page.tsx`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/components/settings/AdminChannelsSection.tsx`
- Modify: `frontend/src/components/settings/AdminChannelsSection.test.tsx`
- Modify: `frontend/src/components/settings/AdminUsersSection.tsx`
- Modify: `frontend/src/components/settings/AdminInvitesSection.tsx`
- Modify: `frontend/src/components/settings/AdminRolesSection.tsx`
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/app/(shell)/library/page.tsx`

**Interfaces:**

```ts
export function hasCapability(
  user: CurrentUser | null,
  capability: string,
): boolean;
```

Settings/module definitions declare `requiredCapability`; channel drafts contain validated name/type and explicit role override decisions.

- [ ] Add failing custom-role tests for member/invite/role/channel, file, media refresh, library management, annotation moderation, voice, and ordinary module visibility.
- [ ] Replace hardcoded Owner/Administrator/Moderator/Librarian role-name checks with `CurrentUser.permissions`; keep display role names informational only.
- [ ] Refactor the already-working Phase 5 channel forms to use the shared capability resolver, preserve drafts across capability refreshes, retain stable `403/409` handling, and remove duplicate role-name policy without changing their CRUD/override behavior.
- [ ] Remove any control whose backend capability/action is not shipped and ensure backend-denied stale permissions trigger auth/capability refresh.
- [ ] Run `cd frontend && npm run test:unit -- src/lib/capabilities.test.ts src/components/settings/AdminChannelsSection.test.tsx && npx playwright test e2e/capabilities.spec.ts --project=chromium`; expected result: custom roles see exactly granted surfaces, Phase 5 channel forms remain functional, and unauthorized calls still fail server-side.
- [ ] Commit with message `fix(frontend): drive administration by capabilities`.

## P6-T2: Repair Mobile Navigation and Responsive Route Layouts

**Closes:** F01

**Files:**

- Create: `frontend/src/components/MobileNavigation.tsx`
- Create: `frontend/src/components/MobileNavigation.test.tsx`
- Create: `frontend/e2e/mobile-layout.spec.ts`
- Modify: `frontend/playwright.config.ts`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/components/VoiceDock.tsx`
- Modify: `frontend/src/styles/app.css`
- Modify: `frontend/src/styles/landing.css`
- Modify: `frontend/src/styles/chat.css`
- Modify: `frontend/src/styles/library.css`
- Modify: `frontend/src/styles/stream.css`
- Modify: `frontend/src/styles/files.css`
- Modify: `frontend/src/styles/settings.css`

**Contract:** At mobile widths, one safe-area-aware top bar and one bottom primary navigation remain visible; a labeled drawer exposes text/voice channels. Each route owns one vertical scroll region and has no horizontal document overflow in portrait or landscape.

- [ ] Add failing screenshot/reachability/overflow tests at 390×844, 412×915, 844×390, 915×412, 768×1024, 1024×768, and 1440×900 for landing, login, chat, files, library/reader, Stream/player/party, Settings/admin, notifications, and empty/error states.
- [ ] Fix cascade order that hides both rail and tab bar; implement the mobile channel/voice drawer and safe-area top/bottom offsets.
- [ ] Assign scroll ownership, min-width zero, wrapping, and responsive panel/table behavior so top bars, landing header, Stream controls, readers, admin tables, and empty states neither clip nor overlap.
- [ ] Preserve active voice/Watch Party state through route and portrait↔landscape orientation changes; assert focus remains on the active control and do not remount heavy clients merely to navigate.
- [ ] Add `mobile-chrome` and `mobile-safari` Playwright projects using approved Android/iOS touch/device descriptors; the layout spec explicitly rotates each project rather than substituting resized desktop contexts.
- [ ] Run `cd frontend && npm run test:unit -- src/components/MobileNavigation.test.tsx && npx playwright test e2e/mobile-layout.spec.ts --project=mobile-chrome --project=mobile-safari`; expected result: portrait and landscape primary actions are reachable, state survives rotation, and `scrollWidth === clientWidth` on every tested route.
- [ ] Commit with message `fix(frontend): complete mobile navigation and layouts`.

## P6-T3: Recover Authentication, Preferences, and Failed Mutations

**Closes:** F02

**Files:**

- Create: `frontend/src/components/ConnectivityBanner.tsx`
- Create: `frontend/src/stores/useAuthStore.test.ts`
- Create: `frontend/src/stores/usePreferencesStore.test.ts`
- Create: `frontend/e2e/failure-recovery.spec.ts`
- Modify: `frontend/src/lib/api.ts`
- Modify: `frontend/src/stores/useAuthStore.ts`
- Modify: `frontend/src/stores/usePreferencesStore.ts`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/components/settings/AppearanceSection.tsx`
- Modify: `frontend/src/components/settings/VoiceAudioSection.tsx`
- Modify: `frontend/src/stores/useChatSessionStore.ts`
- Modify: `frontend/src/components/chat/ThreadComposer.tsx`

**Interfaces:** Auth connectivity is `online`, `reconnecting`, or `error`; `fetchMe` resolves `authenticated`, `anonymous`, or `unreachable`. Preference state keeps `persisted`, `draft`, `saveStatus`, and `saveError` with `retrySave`/`revertDraft`. Each pending chat root/reply stores `{clientMutationId, body, targetID}` and reuses that immutable ID until the server acknowledges the durable message.

- [ ] Add failing tests for authenticated transport/timeout/5xx, explicit `401`, preference optimistic failure/retry/revert, accepted-but-timed-out message/reply POSTs, edit failure, route change during save, and public error/request ID display.
- [ ] Make API errors distinguish HTTP, network, and timeout; preserve last known authenticated user on non-`401` failure and show a nonmodal connectivity banner.
- [ ] Keep persisted and draft preference snapshots; debounce/coalesce saves, roll back only on explicit revert, and surface retry with the draft intact.
- [ ] Clear message/reply/edit composer input only after acknowledgement; retain the Phase 5 client mutation ID with failed root/reply content so retry returns the already-accepted message instead of creating a duplicate.
- [ ] Run `cd frontend && npm run test:unit -- src/stores/useAuthStore.test.ts src/stores/usePreferencesStore.test.ts src/stores/useChatSessionStore.test.ts && npx playwright test e2e/failure-recovery.spec.ts --project=chromium`; expected result: transient failure does not redirect/logout/lose input, timed-out accepted chat POSTs appear exactly once, and only an authoritative `401` logs out.
- [ ] Commit with message `fix(frontend): recover transport and mutation failures`.

## P6-T4: Reconnect and Reconcile Durable Real-Time State

**Closes:** F02

**Files:**

- Create: `frontend/src/lib/reconnectingSocket.ts`
- Create: `frontend/src/lib/reconnectingSocket.test.ts`
- Create: `frontend/e2e/ws-reconnection.spec.ts`
- Modify: `frontend/src/stores/useChatSessionStore.ts`
- Modify: `frontend/src/stores/useChatSessionStore.test.ts`
- Modify: `frontend/src/stores/useNotificationStore.ts`
- Modify: `frontend/src/stores/useWatchPartyStore.ts`
- Modify: `frontend/src/app/(shell)/chat/page.tsx`

**Interface:** Reconnect delay starts at 500ms, doubles to 30s maximum, applies ±20% jitter, pauses while offline/hidden when no active voice/party requires it, and resets after a stable connection. Intentional logout/close never reconnects. Chat persists `lastChannelSequence` per channel and notifications persist `lastEventSequence` per user; each reconnect samples one Phase 5 high-water, pages forward until `nextAfterSequence == highWaterSequence`, then applies buffered socket hints above that mark.

- [ ] Add fake-timer and browser tests for delay bounds/jitter, offline/online, visibility, intentional close, auth rejection, multi-page channel/event sequence gaps, `410 change_cursor_expired` full resync, mutations/deletions while disconnected, duplicate catch-up/socket delivery, Watch versions, and simultaneous store subscribers.
- [ ] Implement one socket controller with explicit states, one physical connection per URL/scope, bounded listeners, and cancellation-safe timers.
- [ ] On connect/reconnect, buffer socket hints, page `/api/v1/channels/{id}/changes` and `/api/v1/events` through their independently sampled high-water marks, refresh affected durable resources/inbox state, persist applied sequences, then drain only buffered hints above each mark; on an expired channel cursor reload current roots/open threads/read state and adopt the returned high-water before draining; reconcile Watch Party from current version/state in parallel.
- [ ] Merge chat by change sequence plus message ID, notifications by event sequence plus notification ID/idempotency key, and Watch state only by increasing version; permission loss aborts catch-up and removes the affected channel without leaking buffered payloads.
- [ ] Run `cd frontend && npm run test:unit -- src/lib/reconnectingSocket.test.ts src/stores/useChatSessionStore.test.ts src/stores/useNotificationStore.test.ts src/stores/useWatchPartyStore.test.ts && npx playwright test e2e/ws-reconnection.spec.ts --project=chromium`; expected result: delays stay bounded, revocation leaks nothing, and multi-page missed changes/notifications arrive exactly once before newer socket hints.
- [ ] Commit with message `fix(realtime): reconnect and reconcile durable state`.

## P6-T5: Meet WCAG 2.2 AA Interaction, Dialog, Search, and Reader Contracts

**Closes:** F03

**Files:**

- Create: `frontend/src/hooks/useDialogFocus.ts`
- Create: `frontend/src/hooks/useDialogFocus.test.ts`
- Create: `frontend/src/components/Dialog.tsx`
- Create: `frontend/src/components/GlobalSearch.tsx`
- Create: `frontend/src/components/GlobalSearch.test.tsx`
- Create: `frontend/e2e/accessibility.spec.ts`
- Create: `frontend/e2e/accessibility-audit.md`
- Create: `frontend/scripts/check-accessibility-evidence.mjs`
- Create: `frontend/scripts/check-accessibility-evidence.test.mjs`
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Modify: `frontend/src/app/page.tsx`
- Modify: `frontend/src/app/login/page.tsx`
- Modify: `frontend/src/app/(shell)/chat/page.tsx`
- Modify: `frontend/src/app/(shell)/files/page.tsx`
- Modify: `frontend/src/app/(shell)/library/page.tsx`
- Modify: `frontend/src/app/(shell)/settings/page.tsx`
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/components/MobileNavigation.tsx`
- Modify: `frontend/src/components/VoiceDock.tsx`
- Modify: `frontend/src/components/chat/ChatAside.tsx`
- Modify: `frontend/src/components/chat/ChatMessage.tsx`
- Modify: `frontend/src/components/chat/EmojiPicker.tsx`
- Modify: `frontend/src/components/chat/MentionAutocomplete.tsx`
- Modify: `frontend/src/components/chat/Reactions.tsx`
- Modify: `frontend/src/components/chat/ShareCard.tsx`
- Modify: `frontend/src/components/chat/SharePicker.tsx`
- Modify: `frontend/src/components/chat/ThreadComposer.tsx`
- Modify: `frontend/src/components/chat/ThreadPanel.tsx`
- Modify: `frontend/src/components/library/BookManageModal.tsx`
- Modify: `frontend/src/components/library/BookReader.tsx`
- Modify: `frontend/src/components/library/EpubReader.tsx`
- Modify: `frontend/src/components/library/PdfReader.tsx`
- Modify: `frontend/src/components/library/AnnotationPanel.tsx`
- Modify: `frontend/src/components/library/AnnotationEditor.tsx`
- Modify: `frontend/src/components/notifications/NotificationInbox.tsx`
- Modify: `frontend/src/components/notifications/NotificationItem.tsx`
- Modify: `frontend/src/components/settings/AdminChannelsSection.tsx`
- Modify: `frontend/src/components/settings/AdminInvitesSection.tsx`
- Modify: `frontend/src/components/settings/AdminRolesSection.tsx`
- Modify: `frontend/src/components/settings/AdminUsersSection.tsx`
- Modify: `frontend/src/components/settings/AppearanceSection.tsx`
- Modify: `frontend/src/components/settings/ProfileSection.tsx`
- Modify: `frontend/src/components/settings/SecuritySection.tsx`
- Modify: `frontend/src/components/settings/VoiceAudioSection.tsx`
- Modify: `frontend/src/components/stream/CreateWatchPartyDialog.tsx`
- Modify: `frontend/src/components/stream/MediaPlayer.tsx`
- Modify: `frontend/src/components/stream/MyListShelf.tsx`
- Modify: `frontend/src/components/stream/WatchPartyPanel.tsx`
- Modify: `frontend/src/styles/app.css`
- Modify: `frontend/src/styles/chat.css`
- Modify: `frontend/src/styles/files.css`
- Modify: `frontend/src/styles/landing.css`
- Modify: `frontend/src/styles/library.css`
- Modify: `frontend/src/styles/notifications.css`
- Modify: `frontend/src/styles/settings.css`
- Modify: `frontend/src/styles/stream.css`
- Modify: `frontend/src/styles/voice.css`

**Contracts:** Dialogs trap focus, close on Escape when safe, restore opener focus, expose name/description/modal semantics, and keep destructive confirmation explicit. Search follows combobox/listbox with active descendant, keyboard selection, and announced result count. Axe scans are an automated regression signal, not proof of WCAG conformance; `accessibility-audit.md` records a dated human audit by route/viewport for keyboard order, focus, semantics, names/states, live announcements, errors, target size, non-pointer alternatives, contrast, reduced motion, reader operation, 200% zoom, and 320-CSS-pixel reflow.

- [ ] Add failing route-by-route axe and interaction tests plus a failing evidence-schema validator; cover keyboard-only operation, focus trap/restore, reader focus, search announcements, hover actions, live status/errors, reduced motion, contrast, target size, 200% zoom, and 320-CSS-pixel reflow at desktop and mobile viewports.
- [ ] Replace clickable `div`/card/results with native buttons/links or complete semantics, implement the search combobox/listbox contract with scoped loading/result/error announcements, keep visible focus, and make every message/card action available without hover or precision pointer input.
- [ ] Migrate all listed modal/picker surfaces to the shared dialog/focus behavior and ensure nested reader/annotation panels restore the correct opener.
- [ ] Add reduced-motion alternatives for vaporwave/background/transition effects and fix AA contrast/zoom reflow without changing canonical themes.
- [ ] Perform the manual audit in installed current stable Chrome and Firefox at 390×844 and 1440×900, record browser version/tester/date/evidence and pass/fail for every matrix cell in `frontend/e2e/accessibility-audit.md`, and fix every known WCAG 2.2 A/AA failure before signing it.
- [ ] Run `cd frontend && node --test scripts/check-accessibility-evidence.test.mjs && npm run test:unit -- src/hooks/useDialogFocus.test.ts src/components/GlobalSearch.test.tsx && npx playwright test e2e/accessibility.spec.ts --project=chromium && npm run check:a11y-evidence`; expected result: automated checks report no detected A/AA regression, the signed manual matrix has no known A/AA failure, focus stays contained/restored, and keyboard-only users complete each primary flow; P6-T7 repeats the automated spec across the certification engines.
- [ ] Commit with message `fix(a11y): meet WCAG interaction and focus contracts`.

## P6-T6: Enforce Lazy Feature Chunks, Narrow Selectors, and Bundle Budget

**Closes:** F04

**Files:**

- Create: `frontend/bundle-budget.json`
- Create: `frontend/scripts/check-bundle-budget.mjs`
- Create: `frontend/scripts/check-bundle-budget.test.mjs`
- Create: `frontend/scripts/clean-build.mjs`
- Modify: `frontend/next.config.ts`
- Modify: `frontend/package.json`
- Modify: `frontend/src/components/AppShell.tsx`
- Modify: `frontend/src/components/VoiceDock.tsx`
- Modify: `frontend/src/stores/useVoiceSessionStore.ts`
- Modify: `frontend/src/app/(shell)/library/page.tsx`
- Modify: `frontend/src/app/(shell)/stream/page.tsx`
- Modify: `frontend/src/app/(shell)/chat/page.tsx`
- Modify: `frontend/src/app/(shell)/files/page.tsx`
- Modify: `frontend/src/app/(shell)/settings/page.tsx`
- Modify: `frontend/src/components/notifications/NotificationInbox.tsx`
- Modify: `frontend/src/components/chat/ThreadPanel.tsx`
- Modify: `frontend/src/components/library/BookReader.tsx`
- Modify: `frontend/src/components/stream/WatchPartyPanel.tsx`

**Budget:** `/chat`, `/files`, `/library`, `/stream`, `/settings`, and the shell bootstrap each load at most 256,000 gzip bytes before interaction. The `/library` and `/stream` initial route budgets exclude reader/player code only when the corresponding controls have not been activated; `livekit-client`, `hls.js`, `pdfjs-dist`, and `epubjs` appear only in their activated feature chunks.

- [ ] Add a failing deterministic manifest analyzer for shell plus `/chat`, `/files`, `/library`, `/stream`, and `/settings` gzip budgets, forbidden heavy-module membership, duplicate framework copies, and missing dynamic boundaries; record current baseline.
- [ ] Dynamically import voice client, media player, PDF reader, and EPUB reader only after the user enters/opens that feature; provide accessible loading/error states.
- [ ] Replace whole-store Zustand subscriptions with narrow selectors/equality functions and isolate high-frequency voice/playback/time state from the app shell.
- [ ] Add `npm run clean:build` and `npm run check:bundle`; parse emitted client manifests/chunks and fail the build on any budget/module violation.
- [ ] Run `cd frontend && node --test scripts/check-bundle-budget.test.mjs && npm ci && npm run clean:build && npm run check:bundle && npm run clean:build && npm run check:bundle`; expected result: analyzer tests pass, both clean builds have the same route/module classification, and shell, `/chat`, `/files`, `/library`, `/stream`, and `/settings` each remain at or below 256,000 gzip bytes before interaction.
- [ ] Commit with message `perf(frontend): enforce lazy chunks and bundle budget`.

## P6-T7: Build the Supported Browser and Responsive Regression Matrix

**Closes:** F05, O04

**Files:**

- Create: `frontend/e2e/fixtures/auth.ts`
- Create: `frontend/e2e/fixtures/data.ts`
- Create: `frontend/e2e/stateful/product.spec.ts`
- Create: `frontend/e2e/browser-projects.spec.ts`
- Create: `frontend/browser-certification.json`
- Create: `frontend/scripts/check-installed-browsers.mjs`
- Create: `frontend/scripts/check-installed-browsers.test.mjs`
- Modify: `frontend/playwright.config.ts`
- Modify: `frontend/e2e/bootstrap.spec.ts`
- Modify: `frontend/e2e/chat.spec.ts`
- Modify: `frontend/e2e/files.spec.ts`
- Modify: `frontend/e2e/library-manage.spec.ts`
- Modify: `frontend/e2e/library.spec.ts`
- Modify: `frontend/e2e/login.spec.ts`
- Modify: `frontend/e2e/smoke.spec.ts`
- Modify: `frontend/e2e/stream-refresh.spec.ts`
- Modify: `frontend/e2e/stream.spec.ts`
- Modify: `frontend/e2e/voice-secure-context.spec.ts`
- Modify: `frontend/e2e/voice.spec.ts`
- Modify: `frontend/e2e/credits.spec.ts`
- Modify: `frontend/e2e/product-truth.spec.ts`
- Modify: `frontend/e2e/notifications.spec.ts`
- Modify: `frontend/e2e/threads.spec.ts`
- Modify: `frontend/e2e/pdf-reader.spec.ts`
- Modify: `frontend/e2e/annotations.spec.ts`
- Modify: `frontend/e2e/my-list.spec.ts`
- Modify: `frontend/e2e/watch-party.spec.ts`
- Modify: `frontend/e2e/capabilities.spec.ts`
- Modify: `frontend/e2e/mobile-layout.spec.ts`
- Modify: `frontend/e2e/failure-recovery.spec.ts`
- Modify: `frontend/e2e/ws-reconnection.spec.ts`
- Modify: `frontend/e2e/accessibility.spec.ts`
- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`

**Projects:** fast `chromium` plus certification `chrome`, `firefox`, `webkit`, `msedge`, `mobile-chrome`, and `mobile-safari`. `chrome`, `firefox`, and `msedge` use installed vendor stable binaries; the `firefox` project requires Mozilla Firefox stable `152.0.6` or newer (the current stable security baseline when this plan was written), rejects ESR/Beta/Developer/Nightly and Playwright's bundled Firefox as certification substitutes, and records the full installed version. Mobile projects use touch/device descriptors, not resized desktop-only emulation, and each mobile spec exercises portrait plus landscape.

- [ ] Pin `@playwright/test` to `1.61.1`, install its Chromium/WebKit test binaries, require installed current Google Chrome, Mozilla Firefox stable `>=152.0.6`, and Microsoft Edge stable, and add failing detector/project assertions that reject a missing/old/incorrect engine, non-stable channel, bundled-Firefox substitution, device, touch mode, orientation, viewport, or unexpected skip.
- [ ] Centralize authenticated fixtures and unique per-worker users/channels/files/parties with deterministic cleanup.
- [ ] Make stateful coverage include auth/invites/roles/socket revocation, chat/threads/notifications, uploads/malware, PDF/EPUB/annotations, media/My List/Watch Party, voice, deletion, and reconnect.
- [ ] Replace fixed sleeps/timing assumptions with observable UI/server state and durable reconciliation; retries remain diagnostic and cannot turn a reproducible failure green.
- [ ] Add screenshot/layout assertions for 390×844, 412×915, 844×390, 915×412, 768×1024, 1024×768, and 1440×900 plus feature empty/loading/error/long-content states; mobile tests rotate within the same authenticated voice/party session.
- [ ] Run `cd frontend && node --test scripts/check-installed-browsers.test.mjs && npm run check:browsers && npm run lint && npm run test:unit && npm run build && npm run test:browsers`; expected result: the report identifies installed stable Chrome/Firefox/Edge versions, Firefox is Mozilla stable `>=152.0.6`, and all seven projects pass portrait/landscape coverage without unexplained skips or correctness retries.
- [ ] Commit with message `test(frontend): add supported browser matrix`.

## Phase 6 Exit Gate

- [ ] Custom roles see exactly their effective capabilities, and channel management UI performs every Phase 5 action.
- [ ] Portrait/landscape mobile and desktop/tablet screenshots have one usable navigation path, no horizontal overflow, no clipped/obscured primary action, and stable voice/party state through rotation.
- [ ] Transport, preference, message, reply, edit, and socket failures preserve authenticated/user state and expose retry.
- [ ] Reconnected sockets reconcile all missed durable state without duplicates or stale Watch playback.
- [ ] Automated accessibility checks report no detected A/AA regression, and the signed manual keyboard, focus, announcement, semantics, target-size, contrast, reduced-motion, reader, 200% zoom, and reflow matrix records no known WCAG 2.2 A/AA failure.
- [ ] Shell bootstrap plus `/chat`, `/files`, `/library`, `/stream`, and `/settings` remain at or below 256,000 gzip bytes before interaction and do not contain unactivated LiveKit/HLS/PDF/EPUB code.
- [ ] Chromium smoke plus current installed stable Chrome, Mozilla Firefox stable `>=152.0.6`, WebKit, installed stable Edge, mobile Chrome, and mobile Safari projects pass the stateful/responsive suite without unexplained skips.
- [ ] Frontend lint/unit/build/bundle checks and the common master verification commands pass.
- [ ] Stop for human review; Phase 7 records this accepted commit as the immutable `0.1.0-alpha.1` predecessor, completes operational implementation through P7-T10, and freezes the final beta candidate only at P7-T11.
