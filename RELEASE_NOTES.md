# Release Notes and AI Handoff

This file documents the local fork changes that should be preserved when another AI or maintainer continues the work. The current active branch is `codex/upstream-20260704-enhanced`; the rollback branch is `back-20260702`.

## Branch Policy

Only keep these two remote branches:

- `codex/upstream-20260704-enhanced`: current feature branch and default working branch.
- `back-20260702`: July 2, 2026 rollback branch. It points to the last known smaller/stable backup snapshot before the July 4 upstream refresh.

Do not keep temporary investigation branches such as header-debug, passthrough-test, or old request-body experiments after their changes have been folded into the current branch.

## 2026-07-04 Current Feature Branch

Branch: `codex/upstream-20260704-enhanced`

Purpose: refresh to the latest upstream code available on July 4, 2026, then reapply the private operational changes needed for Codex subscription channels, usage statistics, dashboard display, and deployment.

### Codex Subscription Relay

Codex subscription channels are detected by both API type and channel type:

- `APITypeCodex`
- `ChannelTypeCodex`
- relay mode `/v1/responses` or `/v1/responses/compact`

For those requests, the relay intentionally keeps the original Codex client request body instead of rebuilding the body through the normal New API conversion path. This preserves Codex-client fields such as `store`, `prompt_cache_key`, turn/thread metadata, and other request-body details that may affect upstream behavior or cache affinity.

The relay also passes through selected Codex client headers while preserving the server-side subscription authentication that identifies the paid account:

- `Originator`
- `Session-Id`
- `Session_id`
- `Thread-Id`
- `User-Agent`
- `X-Client-Request-Id`
- `X-Codex-Beta-Features`
- `X-Codex-Installation-Id`
- `X-Codex-Parent-Thread-Id`
- `X-Codex-Turn-State`
- `X-Codex-Turn-Metadata`
- `X-Codex-Window-Id`
- `X-OAI-Attestation`
- `X-OpenAI-Internal-Codex-Responses-Lite`
- `X-OpenAI-Subagent`

The authentication headers, including subscription `Authorization` and `chatgpt-account-id`, still come from the configured Codex subscription channel. The client-provided authentication is not trusted for upstream billing identity.

Relevant files:

- `relay/responses_handler.go`
- `relay/common/override.go`
- `relay/responses_handler_test.go`
- `relay/common/override_test.go`

### Responses Stream `client_gone` Fix

The July 2 rollback branch had a local fix for false `client_gone` records on Responses SSE streams. That fix is preserved on this branch.

The issue is that `response.completed` is the semantic terminal event for `/v1/responses`. Codex may close the downstream stream immediately after receiving terminal or function-call events while continuing the same Codex session. If New API waits for upstream EOF after those terminal events, the request context can be canceled and falsely recorded as `client_gone`.

Current behavior:

- `response.completed` calls `StreamResult.Done()` and stops scanning without waiting for a later EOF;
- function-call terminal events mark client close as expected;
- expected downstream close is recorded as `handler_stop`, not `client_gone`;
- scanner cleanup closes the upstream response body before waiting for worker goroutines, so blocked reads exit promptly.

Relevant files:

- `relay/channel/openai/relay_responses.go`
- `relay/common/stream_status.go`
- `relay/helper/stream_scanner.go`
- `relay/channel/openai/relay_responses_test.go`
- `relay/common/stream_status_test.go`
- `relay/helper/stream_scanner_test.go`

### 2026-07-18 Responses Large-Context Memory Fix

The production host has 2 GiB RAM and no swap. On July 18, five kernel OOM
events killed `new-api` while several large-context `/v1/responses` streams ran
concurrently. The same rc.21 behavior is reported upstream in issue #6159.

The local fix keeps Codex request and session behavior unchanged while bounding
the response path:

- Responses SSE events are decoded into a minimal usage/item/output view rather
  than the complete response object graph;
- only direct Responses streams use synchronous event handoff, while every
  other stream format keeps the established ten-event buffer;
- Responses SSE frames are written without formatting another full-size payload
  copy, downstream write failures immediately close the upstream body, and a
  terminal event's usage is retained even if that downstream write fails;
- self-use Codex Responses no longer retain the complete generated text solely
  for abnormal-stream token fallback, avoiding another output-sized allocation;
- in self-use mode, only `APITypeCodex` + `ChannelTypeCodex` + `/v1/responses`
  trusts the authoritative `response.completed.usage`, skipping request-time
  tiktoken counting and quota pre-consumption. Final usage logging and postpaid
  settlement still use the upstream usage. The selected channel is read from
  distributor context because request-time counting runs before `ChannelMeta`
  initialization. All other API/channel combinations keep normal local counting
  and pre-consumption;
- Codex original-body forwarding uses a shallow request shell for model mapping
  instead of deep-copying large input/tool raw messages, and built-in tool usage
  metadata is extracted directly from raw JSON without building a generic map
  object graph.

A second production audit on July 18 identified the remaining request-side
amplification and retention root cause. Preserve these additional changes:

- self-use `APITypeCodex` + `ChannelTypeCodex` Responses validation extracts
  only model, stream, max-output and minimal built-in-tool metadata from the
  original JSON; large `input` and function schemas are not copied into
  `json.RawMessage` fields;
- compliant Codex bodies (`instructions` present, `store: false`, no unsupported
  max/temperature fields) are forwarded directly from the original
  `BodyStorage`; sanitization performs no full-body write and no second outbound
  memory storage is created;
- a retry that actually switches away from a Codex channel rehydrates the full
  DTO only for that non-Codex conversion attempt, preserving mixed-channel
  compatibility;
- sensitive-word checking keeps the full request decode path so it still sees
  the original input/instructions; the minimal shell is used only when prompt
  sensitive checking is disabled;
- configured channel model mappings are written into the forwarded Codex JSON;
  an unmapped, already-compliant body remains the true zero-copy fast path;
- each zero-copy upstream attempt opens an independent reader cursor, so a retry
  cannot seek a body reader that a previous HTTP transport may still be closing;
- closing a memory `BodyStorage` clears its backing slice and reader, cleanup
  clears both Gin cache keys and `Request.Body`, and cleanup is deferred so
  panic/abort paths cannot leave a large body referenced by Gin's context pool;
- streaming image partials use synchronous handoff, do not retain the final raw
  base64 event, and write the existing string directly without another
  byte-to-string payload copy;
- the performance page distinguishes current heap/process RSS from cumulative
  `TotalAlloc` and runtime-reserved `Sys`, and exposes HeapInuse/HeapReleased.

The follow-up request-lifetime fix keeps retry behavior before an upstream
response is accepted, but no longer keeps uploaded request bodies alive for the
rest of a successful Codex SSE generation:

- connection failures and non-200 responses still retain the immutable original
  body for same-channel retries and channel switching;
- an HTTP 200 Codex Responses stream commits the relay attempt, closes both the
  original replay storage and any rewritten outbound storage immediately, and
  continues reading/writing the upstream SSE response normally;
- a stream error after that commit is returned on the existing stream and is not
  replayed, avoiding duplicate events or duplicate tool execution;
- releasing the body does not cancel the Gin request context or close the
  upstream response body;
- the distributor and the Codex `prompt_cache_key` affinity lookup share an
  8 KiB-buffered top-level JSON span index, so a disk-backed request is not read
  back into one large Go heap buffer merely to obtain routing metadata;
- required top-level Codex rewrites (`model`, `instructions`, `store`,
  `max_output_tokens`, and `temperature`) are produced as a sectioned stream;
  unchanged large `input`, tool schemas, web-search data, and image-generation
  inputs are copied directly between body stores;
- header-only parameter overrides, including the default Codex affinity
  `pass_headers` rule, run without materializing the request JSON;
- arbitrary nested body overrides, tiered billing expressions that inspect the
  request body, and full sensitive-word inspection retain their compatibility
  paths and may still materialize or retain the complete body when enabled.

The production evidence for this root cause was request-body-specific: the
performance page showed 237.76 MiB across ten active `BodyStorage` buffers while
the corresponding successful `/v1/responses` rows had zero image-generation
calls. A 2 MiB allocation regression fixture now parses into the minimal Codex
request shell with only about 2 KiB allocated; no-op sanitization allocates only
single-digit bytes rather than another body-sized buffer.

The optimization does not change original-body forwarding, Codex subscription
authentication headers, function-call terminal handling, `previous_response_id`,
or cache token fields.

Follow-up production deployment completed on July 18:

- source commit: `3cf170a1`;
- current image/container: `new-api:20260718-uploadfree` /
  `new-api-20260718-uploadfree`;
- current version: `v1.0.0-rc.21-uploadfree-20260718`;
- current Caddy target: `127.0.0.1:3026`;
- runtime memory limit: `GOMEMLIMIT=768MiB`;
- runtime node type remains `NODE_TYPE=slave`;
- request-body disk caching is enabled with the existing 10 MiB threshold and
  1 GiB cache limit, so larger bodies spill to temporary files without changing
  their contents;
- online SQLite backup before the setting change:
  `/opt/new-api/backups/one-api-before-rootfix-20260718-1746.db`;
- rollback image/container: `new-api:20260718-rootfix` /
  `new-api-20260718-rootfix` (kept stopped);
- the superseded July 17 rollback container/image was removed after public
  health verification.

Operational rollback on July 19:

- the `uploadfree` build was rolled back after the operator reported serious
  runtime problems during client testing;
- current image/container: `new-api:20260718-rootfix` /
  `new-api-20260718-rootfix`;
- current Caddy target: `127.0.0.1:3025`;
- `new-api:20260718-uploadfree` / `new-api-20260718-uploadfree` is kept stopped
  for investigation and must not be returned to production without a root-cause
  review and a new test build;
- no database rollback or schema change was needed.

### 2026-07-19 Codex Multi-Agent Stream Preemption Fix

The high `client_gone` count was separated from the request-body memory issue.
Codex multi-agent v2 can intentionally abandon an in-flight Responses stream
when mailbox input arrives after a completed reasoning or assistant commentary
item. This happens before `response.completed`, so immediately closing the
upstream loses the authoritative input/output/cache usage even though the Codex
turn continues normally with a follow-up request.

The Responses relay now handles that client behavior without restoring the
large event queue or long-lived request-body retention:

- only `APITypeCodex` + `ChannelTypeCodex` Responses streams receive a bounded
  two-second terminal grace after downstream cancellation;
- reasoning and assistant-commentary output items mark a following Codex client
  close as an expected handler stop, matching the existing function-call close
  handling;
- no additional events are written after the downstream context is cancelled;
- a trailing `response.completed` is still decoded during the grace period so
  prompt, completion and cached-token usage can be recorded;
- a silent or still-generating upstream is closed when the two-second grace
  expires, so abandoned requests cannot leave a goroutine or response body
  running indefinitely;
- Responses event handoff remains synchronous, preserving the memory bound;
- all non-Codex and non-Responses streams retain immediate client-disconnect
  cleanup;
- per-write SSE deadlines are cleared after each data or ping write. The rc.21
  implementation left the deadline installed, which could turn a single-write
  timeout into a later HTTP/2 stream reset.

The official Codex client expects `response.completed` for token usage, and its
turn-scoped `x-codex-turn-state` is required for sticky routing. Do not remove
that response/request header synchronization as a latency workaround.

Production deployment completed on July 19:

- source commit: `32383c8a`;
- current image/container: `new-api:20260719-preempt` /
  `new-api-20260719-preempt`;
- current version: `v1.0.0-rc.21-preempt-20260719`;
- current Caddy target: `127.0.0.1:3027`;
- runtime memory limit remains `GOMEMLIMIT=768MiB` and node type remains
  `NODE_TYPE=slave`;
- the previous `new-api:20260718-rootfix` container is stopped and retained as
  the immediate rollback while this change is operator-tested;
- the rejected `uploadfree` container/image and the uploaded image tarball were
  removed after the public health check passed.

Diagnostic logging deployment completed later on July 19:

- source changes: `relay/helper/stream_scanner.go` and
  `relay/channel/openai/relay_responses.go`;
- current image/container: `new-api:20260719-diag` /
  `new-api-20260719-diag`;
- current version: `v1.0.0-rc.21-diag-20260719`;
- current Caddy target: `127.0.0.1:3028`;
- diagnostics are written only to the existing application/container logs;
  this change adds no database tables, columns, indexes, migrations, or
  statistics writes;
- logs correlate request ID, upstream request ID, context error, write error,
  expected-close state, grace start/end/expiry, terminal usage, upstream body
  close, and final stream reason without logging request or response payloads.

Operator rollback later on July 19:

- the diagnostic build was removed from the public Caddy route after the
  operator observed rapid RSS growth during testing;
- public traffic is back on `new-api:20260719-preempt` /
  `new-api-20260719-preempt` at `127.0.0.1:3027`;
- `new-api:20260719-diag` remains stopped with its diagnostic log for review;
- no database schema, migration, or statistics change was made during the
  rollback.

Follow-up memory fix prepared on July 19:

- Codex Responses now uses a per-stream inline scanner/handler path; it does
  not serialize separate HTTP requests or conversations;
- the scanner cannot read the next complete SSE event until the current large
  event has been parsed and forwarded, removing the previous read-ahead
  overlap between the scanner goroutine and handler goroutine;
- the Codex Responses handler consumes `scanner.Bytes()` directly and writes
  the payload as bytes, eliminating the additional full-event allocation made
  by `scanner.Text()`;
- all other stream formats retain their existing buffered handler path;
- no database schema, migration, or statistics write was added.

Relevant files:

- `controller/relay.go`
- `dto/openai_response.go`
- `relay/channel/openai/relay_responses.go`
- `relay/helper/common.go`
- `relay/helper/stream_scanner.go`
- `relay/helper/valid_request.go`
- `relay/responses_handler.go`
- `relay/common/override.go`
- `common/body_storage.go`
- `common/gin.go`
- `common/json_stream.go`
- `constant/context_key.go`
- `middleware/distributor.go`
- `middleware/body_cleanup.go`
- `relay/channel/openai/relay_image.go`
- `service/channel_affinity.go`
- `controller/performance.go`

### Failed Channel Retry Cycling

The July 2 failed-channel retry strategy is preserved on this branch.

When an upstream channel returns a retryable error, a single request retries the same selected channel up to `RetryTimes` attempts. If that channel keeps failing, the relay excludes that channel for the rest of the same request and selects the next available candidate from the remaining channels. If all candidates are exhausted, the relay returns the last upstream error instead of repeatedly selecting the same failed channel.

Both selection paths honor the per-request exclusion list:

- memory-cache selection through `GetRandomSatisfiedChannel`;
- direct database selection through `GetChannel`.

This does not change the database schema and does not re-enable failed request persistence in `logs`.

Relevant files:

- `controller/relay.go`
- `service/channel_select.go`
- `model/channel_cache.go`
- `model/ability.go`
- `controller/relay_retry_test.go`
- `model/channel_cache_retry_test.go`

### Channel Affinity and Cache Hit Behavior

The built-in `codex cli trace` affinity rule is normalized so that old database settings continue to work with the new behavior.

Current rule behavior:

- affinity key sources include `prompt_cache_key`, `Session-Id`, `Session_id`, `Thread-Id`, and `X-Client-Request-Id`;
- `param_override_template` is cleared for this rule;
- the rule no longer fills `prompt_cache_key` by mutating the request body;
- cache affinity is handled by channel selection and original request/header preservation.

This is intentionally different from the older body-mutation fix. The old approach copied session id fields into `prompt_cache_key` when missing. The current approach tries to stay closer to the original Codex client request and only uses request metadata for channel affinity.

Relevant files:

- `setting/operation_setting/channel_affinity_setting.go`
- `model/option.go`
- `model/option_test.go`
- `service/channel_affinity_template_test.go`

### Usage Log Statistics

Usage-log statistics now expose and display a complete token summary:

- total usage/quota
- request count
- prompt/input tokens
- completion/output tokens
- total tokens
- cache-hit tokens
- cache-hit rate
- RPM
- TPM

The backend stat endpoints return these fields for both admin and self views:

- `/api/log/stat`
- `/api/log/self/stat`

The common usage-log page shows the statistics in one compact line so Codex cache behavior can be monitored directly from the UI.

Relevant files:

- `controller/log.go`
- `model/log.go`
- `model/log_stat_test.go`
- `web/default/src/features/usage-logs/components/common-logs-stats.tsx`
- `web/default/src/features/usage-logs/types.ts`

### Persisted Log Reduction

To avoid database pressure when an upstream channel is unavailable and clients retry quickly, the logs table now keeps only billable activity:

- successful consume logs (`LogTypeConsume`);
- top-up logs (`LogTypeTopup`);
- refund/adjustment logs (`LogTypeRefund`).

The following events intentionally do not persist to `logs`:

- relay error logs (`LogTypeError`);
- login logs (`LogTypeLogin`);
- admin/manage/audit logs (`LogTypeManage`);
- system setting and verification logs (`LogTypeSystem`).

Failed relay requests still return the error response to the client and still write normal process logs, but they no longer create database rows. Dashboard request counts that come from usage logs therefore count successful consume records only.

Performance metrics now record only successful relay samples. Historical `perf_metrics` rows from earlier versions may still include failed samples in `request_count`; after deployment, normalize those rows by setting `request_count = success_count` for rows where failures had previously been counted.

Admin dashboard model request counts are read from `logs.type = LogTypeConsume` through `/api/log/model-request-counts`. The overview and model analytics panels use that successful-log aggregation for request totals and top-model counts; `perf_metrics` is used only for latency and throughput. This is a read-only aggregation over the existing `logs` table and does not change the database schema.

Production cleanup SQL used with a timestamped SQLite backup:

```sql
DELETE FROM logs WHERE type IN (3, 4, 5, 7);
UPDATE perf_metrics
SET request_count = success_count
WHERE request_count > success_count;
```

After cleanup, compare `logs` consume counts by model with `perf_metrics` successful request counts over the same time window. Small differences can exist while in-memory performance buckets have not flushed yet; after a flush/restart, persisted buckets should match the successful-only policy.

For UI request-count verification, compare the dashboard totals with `logs.type = 2`, not with `perf_metrics.request_count`.

Production data note for the July 5 deployment:

- The deployed image/container is `new-api:20260705-countfix` / `new-api-20260705-countfix`.
- Caddy points `api.kendeji.fun` to `127.0.0.1:3019`.
- Historical `logs` rows were reduced to successful consume logs only.
- Completed-hour `perf_metrics.request_count` was reconciled online from `logs.type = 2` without changing the database schema.
- The current hour can show a DB-only difference because live performance samples stay in process memory until the next flush. The `/api/perf-metrics/summary?hours=all` API merges persisted rows and live buckets, and was verified to match the successful consume log total after reconciliation.
- Do not stop the production container for future stats reconciliation. Use an online SQLite backup and avoid writing current live-bucket rows unless the in-memory bucket has already flushed.
- Hard production taboo: never leave the production container stopped while debugging SQL, scripts, migrations, or dashboard statistics. If a stop is unavoidable, prepare the exact restart and health-check commands first, restart within seconds, verify `/api/status`, and only then continue investigation.

Production deployment note for the July 6 retry-cycling build:

- The deployed image/container is `new-api:20260706-retry` / `new-api-20260706-retry`.
- Caddy points `api.kendeji.fun` to `127.0.0.1:3024`.
- The superseded `new-api-20260706-alpha` container/image was removed after public health verification.
- The July 2 rollback image/container remains `new-api:20260702-live` / `new-api-20260702-live`.
- No database schema changes were made.

Relevant files:

- `model/log.go`
- `controller/log.go`
- `router/api-router.go`
- `controller/relay.go`
- `pkg/perf_metrics/metrics.go`
- `pkg/perf_metrics/types.go`
- `web/default/src/features/dashboard/api.ts`
- `web/default/src/features/dashboard/components/overview/performance-health-panel.tsx`
- `web/default/src/features/dashboard/components/models/performance-overview.tsx`
- `model/log_record_test.go`
- `pkg/perf_metrics/metrics_test.go`

### Request ID and Upstream Request ID

Logs support filtering and display by local request id and upstream request id:

- `request_id`
- `upstream_request_id`

This is useful when correlating New API logs with upstream OpenAI/Codex request ids during cache-hit or intermittent reset investigations.

Relevant files:

- `model/log.go`
- `controller/log.go`
- `web/default/src/features/usage-logs/constants.ts`

### Dashboard Model Analytics

The admin model analytics filter has a custom "recent days" input. It is capped by the earliest available `quota_data` record, so the operator can query from today backwards as far as real data exists instead of being limited to the old 29-day maximum.

This feature uses existing `quota_data`; it does not require a custom database table or schema migration.

Relevant files:

- `controller/usedata.go`
- `model/usedata.go`
- `router/api-router.go`
- `web/default/src/features/dashboard/api.ts`
- `web/default/src/features/dashboard/components/models/models-filter-dialog.tsx`
- `web/default/src/features/dashboard/lib/filters.ts`

### Dashboard Overview Restoration

After the July 4 upstream refresh, the overview page was restored to the previous local behavior:

- the first overview card is `Tokens since launch`;
- the card details include input tokens, output tokens, cache-hit tokens, hit rate, usage, and request count;
- model analytics stat cards show seven cards instead of five:
  - request count
  - usage/quota
  - total tokens
  - input tokens
  - output tokens
  - cache-hit tokens
  - cache-hit rate

Relevant files:

- `web/default/src/features/dashboard/api.ts`
- `web/default/src/features/dashboard/components/overview/summary-cards.tsx`
- `web/default/src/features/dashboard/components/overview/overview-dashboard.tsx`
- `web/default/src/features/dashboard/components/models/log-stat-cards.tsx`
- `web/default/src/features/dashboard/hooks/use-dashboard-config.tsx`
- `web/default/src/features/dashboard/index.tsx`
- `web/default/src/features/dashboard/types.ts`

The dashboard performance panels no longer display a success-rate/health field because failed requests are no longer persisted as request records. They display average latency, throughput, and per-model successful request counts instead.

Relevant files:

- `web/default/src/features/dashboard/components/overview/performance-health-panel.tsx`
- `web/default/src/features/dashboard/components/models/performance-overview.tsx`

### System Info, Health, and Task UI

The July 4 branch keeps the upstream default-frontend system information and operational health pages. These pages are part of the current expected UI and should not be lost during future rebases:

- system instance/status display;
- system task display;
- health/status entry points used by the frontend and deployment checks.

Relevant files:

- `web/default/src/features/system-info/index.tsx`
- `web/default/src/features/system-info/components/system-instances-panel.tsx`
- `web/default/src/features/system-info/components/system-tasks-panel.tsx`
- `web/default/src/routes/_authenticated/system-info/index.tsx`
- `controller/system_info.go`
- `controller/system_task.go`
- `controller/system_task_handlers.go`
- `service/system_instance.go`
- `service/system_task.go`

### Build and Deployment Notes

Production builds should be compiled locally for `linux/amd64`; do not build on the small production server.

The working deployment pattern used for the July 4 build:

1. Build both frontends locally.
2. Cross-compile the Go binary locally:

   ```bash
   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOEXPERIMENT=greenteagc go build \
     -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$(cat VERSION)'" \
     -o build/new-api-linux-amd64
   ```

3. Package the binary into a small Alpine-based image.
4. Upload the image tarball to the server.
5. Run `docker load` on the server.
6. Start a new container on an unused localhost port.
7. Health-check `/api/status`.
8. Switch Caddy to the new port.
9. Keep only the current container/image and the July 2 rollback container/image.

Current server naming convention:

- current image: `new-api:20260718-rootfix`
- current container: `new-api-20260718-rootfix`
- current Caddy target: `127.0.0.1:3025`
- investigation image: `new-api:20260718-uploadfree`
- investigation container: `new-api-20260718-uploadfree` (stopped)

Image/container names should stay short: `new-api` + date + one word.

Production container startup parameters must stay generic:

- entrypoint: `/new-api`
- command: none
- no token-specific or route-specific startup parameters
- do not add `anyrouter` through command args, env vars, labels, links, or extra hosts
- `anyrouter` in usage logs/token names is database/runtime data, not a Docker startup route

The current `new-api-20260718-rootfix` container was inspected after deployment and has no `anyrouter` startup route.

### Validation Used

The July 4 feature branch was validated with:

```bash
cd web/default && bun run i18n:sync
cd web/default && bun run typecheck
cd web/default && DISABLE_ESLINT_PLUGIN=true VITE_REACT_APP_VERSION=$(cat ../../VERSION) bun run build
cd web/classic && VITE_REACT_APP_VERSION=$(cat ../../VERSION) bun run build
go test ./model ./relay ./relay/common ./setting/operation_setting ./service
```

The server deployment was also checked with:

```bash
curl -fsS http://127.0.0.1:<port>/api/status
curl -fsS https://api.kendeji.fun/api/status
```

The July 6 retry-cycling build was validated with:

```bash
go test ./controller ./model ./service
make build-all-web
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOEXPERIMENT=greenteagc go build \
  -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$(cat VERSION)'" \
  -o build/new-api-linux-amd64
docker buildx build --platform linux/amd64 --load -f build/Dockerfile.runtime -t new-api:20260706-retry .
curl -fsS http://127.0.0.1:3024/api/status
curl -fsS https://api.kendeji.fun/api/status
```

## 2026-07-02 Rollback Branch

Branch: `back-20260702`

Purpose: preserve the pre-July-4 rollback snapshot.

This branch includes the admin dashboard custom recent-day range work that was active on July 2, 2026. It should be kept as a fallback and not used for new feature work.

Known differences from the current feature branch:

- it does not include the July 4 upstream refresh;
- it does not include the current Codex original-body passthrough implementation;
- it does not include the restored July 4 overview token-stat UI changes;
- it should remain available only for rollback.

## Temporary Work That Should Stay Removed

The following types of changes were investigation-only and should not be kept as normal branches:

- full request/response header debug logging;
- temporary Codex header comparison builds;
- old experiments that mutated request bodies to fill `prompt_cache_key`.

If similar debugging is needed again, create a new temporary branch, deploy it briefly, download logs locally, then remove the branch and server artifacts after the investigation.
