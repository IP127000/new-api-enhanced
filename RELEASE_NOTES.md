# New API Enhanced v1.0.0-enhanced.2-routefix1-dashboard1-keyreveal1-ui1

Dashboard, key-management, and production UI polish release.

## Enhancements

1. **All-time overview health**: the overview page now uses all-time performance metrics, matching the historical Token, spend, and request totals shown on the same page.
2. **Range-aware model dashboard**: the data dashboard performance panel follows the selected statistics range and keeps request, cost, input, output, cache, and hit-rate metrics together.
3. **Overview layout polish**: usage cards, cache details, performance health, and top-model rows were tightened for better alignment and readability on the admin dashboard.
4. **Channel key reveal workflow**: administrators can reveal saved channel keys from the channel editor without the extra secure-verification dialog.

## Fixes

1. **Success-rate denominator consistency**: performance success rates are calculated from the matching model/request population instead of a mismatched aggregate.
2. **Pre-consume refund isolation**: failed-request refund handling now copies only the fields needed for refund settlement before running the asynchronous compensation path.
3. **Dashboard copy accuracy**: overview text now describes Token usage, spend, and request volume instead of balance.

## Verification

```bash
cd web/default && bun run typecheck
cd web/default && bunx oxlint -c .oxlintrc.json src/features/dashboard/components/overview/performance-health-panel.tsx src/features/dashboard/components/overview/summary-cards.tsx src/features/dashboard/components/ui/stat-card.tsx
make build-all-frontends
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=v1.0.0-enhanced.2-routefix1-dashboard1-keyreveal1-ui1'" -o new-api .
```

## Deployment

This version was deployed with the binary-first production flow: frontend assets and the Linux amd64 binary were built locally, uploaded as a compressed artifact, verified by SHA-256 checksum, wrapped into a lightweight Docker image, health-checked on a standby local port, then switched through Caddy after configuration validation.

---

# New API Enhanced v1.0.0-enhanced.2-routefix1

Route failover release for ChatGPT Subscription (Codex) channel groups.

## Enhancements

1. **Per-channel retry budget**: a channel now consumes the configured retry budget before the request moves to another available channel.
2. **Request-local channel exhaustion**: channels that have exhausted their retries are skipped for the rest of the current request, avoiding repeated routing to the same failing channel.
3. **Affinity refresh on successful fallback**: when failover succeeds on a later channel, channel affinity is updated to that final successful channel.
4. **Broader regression coverage**: tests cover three-channel and five-channel fallback, including affinity starting from a non-primary channel.

## Verification

```bash
go test ./model ./service ./controller
go test ./relay/...
```

## Compatibility

This release preserves existing channel priority, weight, group, model, path filtering, and affinity behavior. The channel exhaustion list is request-local only; a new client request starts from affinity or normal channel selection again.

---

# New API Enhanced v1.0.0-enhanced.1

Initial public release of New API Enhanced as an independent downstream build.

## Enhancements

1. **Codex stream stability**: corrects Responses API termination handling at tool-call boundaries, preventing normal stream completion from being reported as abnormal and reducing false `client_gone` and scanner error reports.
2. **Subscription-channel cache optimization**: keeps ChatGPT Subscription (Codex) requests channel-affine and synchronizes `Session-Id`, `Session_id`, and `prompt_cache_key`, reducing cache loss from channel splitting or inconsistent cache identity.
3. **Cache metrics visibility**: usage logs show cache-hit tokens and hit rate.

## Verification

```bash
go test ./service ./relay/common ./relay/channel/codex ./setting/operation_setting
```

## License & Attribution

This is a modified downstream build based on `QuantumNous/new-api`.

Original project:

```text
https://github.com/QuantumNous/new-api
```

New API Enhanced preserves the original New API / QuantumNous attribution,
NOTICE terms, and AGPLv3 obligations. It is not the official upstream New API
repository.
