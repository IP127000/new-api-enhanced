# New API Enhanced

Language: [中文](./README.md) | **English**

New API Enhanced is a downstream enhanced distribution based on
`QuantumNous/new-api`, focused on production Codex and ChatGPT Subscription
proxy scenarios with stronger stream stability, better subscription-channel
prompt cache hit rates, and clearer cache metrics.

This repository is independently maintained and is not the official upstream
New API repository. The original New API / QuantumNous attribution, upstream
project link, NOTICE terms, and AGPLv3 obligations are preserved.

## Enhancements

1. **Codex stream stability**: corrects Responses API termination handling at
   tool-call boundaries, preventing normal stream completion from being reported
   as abnormal and reducing false `client_gone` and scanner error reports.
2. **Subscription-channel cache optimization**: keeps ChatGPT Subscription
   (Codex) requests channel-affine and synchronizes `Session-Id`, `Session_id`,
   and `prompt_cache_key`, reducing cache loss from channel splitting or
   inconsistent cache identity.
3. **Cache metrics visibility**: usage logs show cache-hit tokens and hit rate.

## Usage

### Docker Compose

```bash
git clone https://github.com/IP127000/new-api-enhanced.git
cd new-api-enhanced
docker compose up -d --build
```

Then open:

```text
http://localhost:3000
```

The default `docker-compose.yml` builds this repository and starts New API
Enhanced, PostgreSQL, and Redis. Change database passwords, Redis passwords,
and `SESSION_SECRET` before production use.

### Docker

```bash
git clone https://github.com/IP127000/new-api-enhanced.git
cd new-api-enhanced
docker build -t new-api-enhanced:latest .

docker run --name new-api-enhanced -d --restart always \
  -p 3000:3000 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)/data:/data" \
  new-api-enhanced:latest
```

Configure MySQL, PostgreSQL, or Redis with environment variables when needed:

```bash
SQL_DSN="user:password@tcp(mysql:3306)/new-api"
REDIS_CONN_STRING="redis://:password@redis:6379"
SESSION_SECRET="replace-with-a-random-secret"
```

### Upgrade

```bash
git pull
docker compose up -d --build
```

Private host mappings, internal addresses, log paths, proxy arguments, secrets,
and subscription credentials should be kept in server-local configuration,
Compose overrides, environment variables, secret managers, or deployment
platforms. They should not be committed to the public repository.

## Operational Guidance

- Enable channel affinity for ChatGPT Subscription (Codex) channels and confirm
  that group, model, and channel ability state are consistent.
- If the database already contains customized Codex affinity templates, confirm
  that they synchronize `Session-Id`, `Session_id`, and `prompt_cache_key`.
- Use Redis in production. Multi-node deployments should use the same
  `SESSION_SECRET`.
- Public repositories should not contain API keys, subscription credentials,
  private host mappings, internal addresses, private deployment commands, or
  server handoff documents.

## Verification

```bash
go test ./service ./relay/common ./relay/channel/codex ./setting/operation_setting
```

After startup:

```bash
curl http://localhost:3000/api/status
```

## Attribution & Compliance

New API Enhanced is a modified version based on `QuantumNous/new-api`.
The upstream project is available at:

```text
https://github.com/QuantumNous/new-api
```

This project is distributed under the GNU Affero General Public License v3.0.
Any deployment, modification, redistribution, or network service operation must
comply with AGPLv3, including the obligation to provide the corresponding
modified source code to network users where applicable.

This project preserves the upstream AGPLv3 Section 7 additional terms stated in
the NOTICE file. Modified versions that present a user interface should preserve
reasonable legal notices, author attribution, and the upstream project link,
including but not limited to:

```text
Frontend design and development by New API contributors.
https://github.com/QuantumNous/new-api
```

Modified versions must not misrepresent the origin of the software, must clearly
identify themselves as downstream modified versions, and must preserve the New
API and QuantumNous attribution, upstream project link, LICENSE, NOTICE, and
third-party license notices. The New API Enhanced name identifies this
downstream distribution and does not represent an official upstream release.

This project is intended for lawfully authorized AI API gateway, internal
organization authentication, multi-model management, usage analytics, cost
accounting, and private deployment scenarios. Users are responsible for ensuring
that they have valid authorization for upstream APIs, accounts, keys, quotas,
model services, and interfaces, and for complying with upstream terms, platform
rules, and applicable laws. Public generative AI service operators are
responsible for any applicable filing, licensing, content safety, real-name,
logging, tax, payment, and upstream authorization obligations.
