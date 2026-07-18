# Migration Report: Local Enhanced Branch with Official rc.21

## Baselines

- Official repository: `https://github.com/QuantumNous/new-api.git`
- Official release: `v1.0.0-rc.21`
- Official release commit: `bde9b2f44887d34ec54799ae191d50f97914359e`
- Local enhanced commit: `b9fe8082ab66f5add816bd0347115ec9b1df6d19`
- Common ancestor: `722d0366b727b82fced878af902e48363626b2fb`

At the comparison baseline, the local branch had 15 unique commits touching 68 files. Official rc.21 had 83 unique commits touching 387 files. Thirty files were modified on both sides; the two tips differed in 425 files.

## Strategy

This directory starts from the current local enhanced branch and merges official rc.21 into it. Non-conflicting official changes are absorbed directly. Conflicts preserve the local dashboard composition and operational behavior, while backend conflicts use combined implementations where dropping either side would lose functionality.

Notable combined resolutions:

- Codex original request-body/header passthrough, configured subscription authentication, affinity normalization, stream completion handling, successful-only log statistics, and failed-channel cycling are preserved.
- Official Advanced Custom path-and-model routing is combined with per-request failed-channel exclusion.
- Official stream write deadlines, SSE response headers, billing hardening, quota saturation protection, SSRF protection, subscription locking/reset, GPT-5.6 pricing, text protocol conversion, Playground, pricing workflow, system cleanup, and zh-TW/i18n changes are merged.
- The local dashboard composition remains the default, including historical token/input/output/cache cards, successful request counts, cache-hit rate, and local performance presentation.
- The local site icons remain in this local-first variant.
- The Classic frontend keeps the local `date-fns` package-root alias required for a clean workspace build.

## Validation

- `bun run typecheck` in `web/default`: passed.
- Production build in `web/default`: passed.
- Production build in `web/classic`: passed.
- `go test ./...`: passed.
- Repository-wide `bun run lint`: not clean; it reports many existing lint errors across unrelated official and inherited files. Type checking and both production builds are clean.

## Post-audit remediation

The independent review identified and this branch now fixes the shared Codex header-sync gap:

- automatic passthrough now includes `Thread_id`, `X-OpenAI-Memgen-Request`, and `X-ResponsesAPI-Include-Timing-Metrics`;
- the Default frontend again offers an optional Codex CLI header-passthrough preset, without restoring it to the default affinity rule;
- Usage Logs statistics now display request count;
- an upstream HTTP integration test verifies the original body, trace headers, and configured server subscription authentication together.

The local dashboard composition, success-only performance samples, local icons, and Classic `strictPort` behavior remain intentional local-first choices.

## Intended Use

Use this tree when the priority is minimum behavioral and visual change from the deployed enhanced branch while still receiving the complete official rc.21 implementation set.
