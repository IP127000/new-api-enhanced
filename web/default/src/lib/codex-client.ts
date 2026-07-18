/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

export const CODEX_CHANNEL_TYPE = 57

export const CODEX_CLI_HEADER_PASSTHROUGH_HEADERS = [
  'Originator',
  'Session-Id',
  'Session_id',
  'Thread-Id',
  'Thread_id',
  'User-Agent',
  'X-Client-Request-Id',
  'X-Codex-Beta-Features',
  'X-Codex-Installation-Id',
  'X-Codex-Parent-Thread-Id',
  'X-Codex-Turn-State',
  'X-Codex-Turn-Metadata',
  'X-Codex-Window-Id',
  'X-OAI-Attestation',
  'X-OpenAI-Memgen-Request',
  'X-OpenAI-Internal-Codex-Responses-Lite',
  'X-OpenAI-Subagent',
  'X-ResponsesAPI-Include-Timing-Metrics',
]

export const CODEX_CLI_HEADER_PASSTHROUGH_TEMPLATE = {
  operations: [
    {
      mode: 'pass_headers',
      value: [...CODEX_CLI_HEADER_PASSTHROUGH_HEADERS],
      keep_origin: true,
    },
  ],
}

export const CODEX_CLI_HEADER_PASSTHROUGH_JSON = JSON.stringify(
  CODEX_CLI_HEADER_PASSTHROUGH_TEMPLATE,
  null,
  2
)
