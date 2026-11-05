#!/usr/bin/env python3
"""End-to-end Codex app-server realtime smoke test through New API.

The API key is read only from REALTIME_NEW_API_KEY. The script creates an
isolated CODEX_HOME, starts app-server over stdio JSON-RPC, opens a V3
realtime conversation, appends one text utterance, and saves returned PCM16
audio as a WAV file.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import queue
import shutil
import subprocess
import tempfile
import threading
import time
import wave
from collections import deque
from pathlib import Path
from typing import Any


class AppServer:
    def __init__(self, command: list[str], environment: dict[str, str]) -> None:
        self._process = subprocess.Popen(
            command,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            bufsize=1,
            env=environment,
        )
        self._messages: queue.Queue[dict[str, Any]] = queue.Queue()
        self._notifications: deque[dict[str, Any]] = deque()
        self._stderr: deque[str] = deque(maxlen=40)
        self._request_id = 0
        self._write_lock = threading.Lock()
        threading.Thread(target=self._read_stdout, daemon=True).start()
        threading.Thread(target=self._read_stderr, daemon=True).start()

    def _read_stdout(self) -> None:
        assert self._process.stdout is not None
        for line in self._process.stdout:
            line = line.strip()
            if not line:
                continue
            try:
                message = json.loads(line)
            except json.JSONDecodeError:
                continue
            self._messages.put(message)

    def _read_stderr(self) -> None:
        assert self._process.stderr is not None
        for line in self._process.stderr:
            self._stderr.append(line.rstrip())

    def _send(self, message: dict[str, Any]) -> None:
        payload = json.dumps(message, ensure_ascii=False, separators=(",", ":"))
        with self._write_lock:
            if self._process.poll() is not None or self._process.stdin is None:
                raise RuntimeError(self.failure_detail("app-server stopped"))
            self._process.stdin.write(payload + "\n")
            self._process.stdin.flush()

    def notify(self, method: str, params: dict[str, Any] | None = None) -> None:
        message: dict[str, Any] = {"method": method}
        if params is not None:
            message["params"] = params
        self._send(message)

    def request(
        self, method: str, params: dict[str, Any], timeout: float = 30.0
    ) -> dict[str, Any]:
        self._request_id += 1
        request_id = self._request_id
        self._send({"id": request_id, "method": method, "params": params})
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            message = self._next_message(deadline - time.monotonic())
            if message is None:
                break
            if message.get("id") == request_id and "method" not in message:
                error = message.get("error")
                if error:
                    raise RuntimeError(
                        f"{method} failed: {error.get('message', 'unknown JSON-RPC error')}"
                    )
                result = message.get("result")
                return result if isinstance(result, dict) else {}
            self._ingest(message)
        raise TimeoutError(self.failure_detail(f"timed out waiting for {method}"))

    def wait_notification(
        self, methods: set[str], timeout: float
    ) -> dict[str, Any] | None:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            for index, message in enumerate(self._notifications):
                if message.get("method") in methods:
                    del self._notifications[index]
                    return message
            message = self._next_message(deadline - time.monotonic())
            if message is None:
                break
            self._ingest(message)
        return None

    def _next_message(self, timeout: float) -> dict[str, Any] | None:
        if timeout <= 0:
            return None
        try:
            return self._messages.get(timeout=timeout)
        except queue.Empty:
            return None

    def _ingest(self, message: dict[str, Any]) -> None:
        if "method" in message and "id" in message:
            if message.get("method") == "currentTime/read":
                self._send(
                    {
                        "id": message["id"],
                        "result": {"currentTimeAt": int(time.time())},
                    }
                )
            else:
                self._send(
                    {
                        "id": message["id"],
                        "error": {
                            "code": -32601,
                            "message": f"unsupported smoke-test client request: {message.get('method')}",
                        },
                    }
                )
            return
        if "method" in message:
            self._notifications.append(message)

    def failure_detail(self, prefix: str) -> str:
        detail = " | ".join(self._stderr)
        return f"{prefix}: {detail[-4000:]}" if detail else prefix

    def close(self) -> None:
        if self._process.poll() is None:
            self._process.terminate()
            try:
                self._process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self._process.kill()
                self._process.wait(timeout=5)


def write_config(home: Path, base_url: str) -> None:
    config = f'''model = "gpt-5.6-sol"
model_provider = "realtime_test"
model_reasoning_effort = "low"
approval_policy = "never"
sandbox_mode = "workspace-write"
experimental_realtime_ws_model = "gpt-realtime-1.5"

[realtime]
version = "v2"
type = "conversational"
transport = "websocket"
voice = "marin"

[model_providers.realtime_test]
name = "Kendeji Realtime Test"
base_url = "{base_url.rstrip('/')}"
env_key = "REALTIME_NEW_API_KEY"
http_headers = {{ "x-openai-actor-authorization" = "realtime-harness-smoke" }}
supports_websockets = false
wire_api = "responses"

[features]
realtime_conversation = true
'''
    home.mkdir(parents=True, exist_ok=True)
    (home / "config.toml").write_text(config, encoding="utf-8")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--base-url", default="https://realtime.kendeji.fun/v1"
    )
    parser.add_argument(
        "--output", default="/private/tmp/codex-realtime-smoke.wav"
    )
    parser.add_argument("--timeout", type=float, default=75.0)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    api_key = os.environ.get("REALTIME_NEW_API_KEY", "").strip()
    if not api_key:
        raise SystemExit("REALTIME_NEW_API_KEY is required")
    codex = shutil.which("codex")
    if not codex:
        raise SystemExit("codex executable is required")

    with tempfile.TemporaryDirectory(prefix="codex-realtime-smoke-") as root:
        root_path = Path(root)
        home = root_path / "home"
        workspace = root_path / "workspace"
        workspace.mkdir()
        write_config(home, args.base_url)
        environment = os.environ.copy()
        environment["CODEX_HOME"] = str(home)
        environment["TMPDIR"] = str(root_path / "tmp")
        Path(environment["TMPDIR"]).mkdir()

        server = AppServer(
            [codex, "app-server", "--listen", "stdio://"], environment
        )
        audio_chunks: list[bytes] = []
        sample_rate = 24000
        channels = 1
        transcript = ""
        realtime_started = False
        try:
            server.request(
                "initialize",
                {
                    "clientInfo": {
                        "name": "realtime-smoke",
                        "title": "Realtime harness smoke test",
                        "version": "1.0",
                    },
                    "capabilities": {"experimentalApi": True},
                },
            )
            server.notify("initialized")
            thread_result = server.request(
                "thread/start",
                {
                    "cwd": str(workspace),
                    "model": "gpt-5.6-sol",
                    "modelProvider": "realtime_test",
                    "approvalPolicy": "never",
                    "sandbox": "workspace-write",
                    "developerInstructions": "Reply in concise Chinese.",
                    "ephemeral": True,
                },
            )
            thread_id = thread_result.get("thread", {}).get("id", "")
            if not thread_id:
                raise RuntimeError("thread/start did not return a thread id")

            server.request(
                "thread/realtime/start",
                {
                    "threadId": thread_id,
                    "model": "gpt-realtime-1.5",
                    "outputModality": "audio",
                    "includeStartupContext": False,
                    "prompt": "You are a realtime voice transport test. Speak concise Chinese.",
                    "transport": {"type": "websocket"},
                    "version": "v2",
                    "voice": "marin",
                },
            )
            started = server.wait_notification(
                {"thread/realtime/started", "thread/realtime/error"}, 30.0
            )
            if started is None:
                raise TimeoutError(server.failure_detail("realtime start notification timed out"))
            if started.get("method") == "thread/realtime/error":
                raise RuntimeError(
                    f"realtime start failed: {started.get('params', {}).get('message', 'unknown error')}"
                )
            realtime_started = True

            server.request(
                "thread/realtime/appendSpeech",
                {
                    "threadId": thread_id,
                    "text": "请只用中文说：实时语音中转测试成功。",
                },
            )

            deadline = time.monotonic() + args.timeout
            while time.monotonic() < deadline:
                event = server.wait_notification(
                    {
                        "thread/realtime/outputAudio/delta",
                        "thread/realtime/transcript/delta",
                        "thread/realtime/transcript/done",
                        "thread/realtime/error",
                        "thread/realtime/closed",
                    },
                    min(5.0, deadline - time.monotonic()),
                )
                if event is None:
                    continue
                method = event.get("method")
                params = event.get("params", {})
                if method == "thread/realtime/error":
                    raise RuntimeError(
                        f"realtime stream failed: {params.get('message', 'unknown error')}"
                    )
                if method == "thread/realtime/outputAudio/delta":
                    audio = params.get("audio", {})
                    data = audio.get("data", "")
                    if data:
                        audio_chunks.append(base64.b64decode(data))
                    sample_rate = int(audio.get("sampleRate") or sample_rate)
                    channels = int(audio.get("numChannels") or channels)
                elif method == "thread/realtime/transcript/delta":
                    transcript += str(params.get("delta", ""))
                elif method == "thread/realtime/transcript/done":
                    transcript = str(params.get("text", transcript))
                    if audio_chunks:
                        break
                elif method == "thread/realtime/closed":
                    break

            if not audio_chunks:
                raise RuntimeError(server.failure_detail("no realtime output audio received"))

            output = Path(args.output)
            output.parent.mkdir(parents=True, exist_ok=True)
            with wave.open(str(output), "wb") as wav:
                wav.setnchannels(channels)
                wav.setsampwidth(2)
                wav.setframerate(sample_rate)
                wav.writeframes(b"".join(audio_chunks))
            print(
                json.dumps(
                    {
                        "ok": True,
                        "transport": "websocket-via-new-api",
                        "base_url": args.base_url,
                        "audio_bytes": sum(map(len, audio_chunks)),
                        "sample_rate": sample_rate,
                        "channels": channels,
                        "transcript": transcript,
                        "output": str(output),
                    },
                    ensure_ascii=False,
                )
            )
            return 0
        finally:
            if realtime_started:
                try:
                    server.request(
                        "thread/realtime/stop", {"threadId": thread_id}, timeout=5.0
                    )
                except Exception:
                    pass
            server.close()


if __name__ == "__main__":
    raise SystemExit(main())
