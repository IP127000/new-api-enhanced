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
