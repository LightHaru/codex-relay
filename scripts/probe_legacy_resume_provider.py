#!/usr/bin/env python3
"""Probe a legacy Codex rollout after switching its app-server provider.

The first app-server writes a real, persisted rollout through a fake legacy
provider. A second app-server opens the same CODEX_HOME with Relay's custom
provider and resumes the old thread. The probe is local-only and has no
credentials; it reports which fake endpoint receives the resumed turn.
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import subprocess
import tempfile
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", required=True, type=Path)
    parser.add_argument("--timeout", type=float, default=45.0)
    return parser.parse_args()


class ProviderState:
    def __init__(self, name: str) -> None:
        self.name = name
        self.lock = threading.Lock()
        self.requests: list[dict[str, Any]] = []


def response_sse(text: str) -> bytes:
    response_id, item_id = "resp_" + uuid.uuid4().hex, "msg_" + uuid.uuid4().hex
    base = {
        "id": response_id, "object": "response", "created_at": int(time.time()),
        "status": "in_progress", "error": None, "incomplete_details": None,
        "instructions": None, "max_output_tokens": None, "model": "relay-resume-probe",
        "output": [], "parallel_tool_calls": True, "previous_response_id": None,
        "reasoning": {"effort": "medium", "summary": None}, "store": False,
        "temperature": None, "text": {"format": {"type": "text"}}, "tool_choice": "auto",
        "tools": [], "top_p": None, "truncation": "disabled", "usage": {
            "input_tokens": 1, "output_tokens": 1, "total_tokens": 2,
            "input_tokens_details": {"cached_tokens": 0},
            "output_tokens_details": {"reasoning_tokens": 0},
        }, "user": None, "metadata": {},
    }
    item = {"id": item_id, "type": "message", "status": "completed", "role": "assistant",
            "content": [{"type": "output_text", "text": text, "annotations": []}]}
    events = [
        ("response.created", {"type": "response.created", "sequence_number": 0, "response": base}),
        ("response.output_item.added", {"type": "response.output_item.added", "sequence_number": 1,
         "output_index": 0, "item": {**item, "status": "in_progress", "content": []}}),
        ("response.content_part.added", {"type": "response.content_part.added", "sequence_number": 2,
         "item_id": item_id, "output_index": 0, "content_index": 0,
         "part": {"type": "output_text", "text": "", "annotations": []}}),
        ("response.output_text.delta", {"type": "response.output_text.delta", "sequence_number": 3,
         "item_id": item_id, "output_index": 0, "content_index": 0, "delta": text, "logprobs": []}),
        ("response.output_text.done", {"type": "response.output_text.done", "sequence_number": 4,
         "item_id": item_id, "output_index": 0, "content_index": 0, "text": text, "logprobs": []}),
        ("response.content_part.done", {"type": "response.content_part.done", "sequence_number": 5,
         "item_id": item_id, "output_index": 0, "content_index": 0,
         "part": item["content"][0]}),
        ("response.output_item.done", {"type": "response.output_item.done", "sequence_number": 6,
         "output_index": 0, "item": item}),
    ]
    completed = {**base, "status": "completed", "output": [item]}
    events.append(("response.completed", {"type": "response.completed", "sequence_number": 7,
                   "response": completed}))
    return "".join(
        f"event: {event}\ndata: {json.dumps(payload, separators=(',', ':'))}\n\n"
        for event, payload in events
    ).encode()


def handler_for(state: ProviderState):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def log_message(self, *_args: Any) -> None:
            return

        def do_POST(self) -> None:  # noqa: N802
            length = int(self.headers.get("Content-Length", "0"))
            raw = self.rfile.read(length)
            try:
                body = json.loads(raw)
            except json.JSONDecodeError:
                body = {}
            with state.lock:
                state.requests.append({
                    "provider": state.name,
                    "path": self.path,
                    "model": str(body.get("model") or ""),
                    "stream": bool(body.get("stream")),
                })
            encoded = response_sse(state.name.upper() + "_RESUME_PROBE_OK")
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.send_header("Connection", "close")
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)
            self.wfile.flush()
            self.close_connection = True

    return Handler


def reader(stream: Any, output: queue.Queue[dict[str, Any]]) -> None:
    for line in iter(stream.readline, ""):
        try:
            output.put(json.loads(line))
        except json.JSONDecodeError:
            continue


def send(process: subprocess.Popen[str], message: dict[str, Any]) -> None:
    assert process.stdin is not None
    process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
    process.stdin.flush()


def wait_for(output: queue.Queue[dict[str, Any]], predicate: Any, timeout: float) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            message = output.get(timeout=min(0.5, deadline - time.monotonic()))
        except queue.Empty:
            continue
        if predicate(message):
            return message
    raise TimeoutError("timed out waiting for app-server protocol evidence")


def run_process(executable: Path, home: Path, args: list[str], actions: Any, timeout: float,
                extra_env: dict[str, str] | None = None) -> None:
    environment = os.environ.copy()
    environment["CODEX_HOME"] = str(home)
    environment["CODEX_SQLITE_HOME"] = str(home)
    if extra_env:
        environment.update(extra_env)
    process = subprocess.Popen(
        [str(executable), *args], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace", bufsize=1,
        env=environment,
    )
    assert process.stdout is not None
    output: queue.Queue[dict[str, Any]] = queue.Queue()
    threading.Thread(target=reader, args=(process.stdout, output), daemon=True).start()
    try:
        actions(process, output, timeout)
    finally:
        if process.stdin is not None:
            process.stdin.close()
        try:
            process.wait(timeout=8)
        except subprocess.TimeoutExpired:
            process.terminate()
            try:
                process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=8)


def main() -> int:
    args = parse_args()
    executable = args.executable.resolve(strict=True)
    legacy = ProviderState("legacy")
    relay = ProviderState("relay_pool")
    legacy_server = ThreadingHTTPServer(("127.0.0.1", 0), handler_for(legacy))
    relay_server = ThreadingHTTPServer(("127.0.0.1", 0), handler_for(relay))
    threading.Thread(target=legacy_server.serve_forever, daemon=True).start()
    threading.Thread(target=relay_server.serve_forever, daemon=True).start()

    with tempfile.TemporaryDirectory(prefix="codex-relay-legacy-resume-") as directory:
        root = Path(directory)
        home = root / "codex-home"
        workspace = root / "workspace"
        home.mkdir()
        workspace.mkdir()

        def config(provider: str, url: str, env_key: str) -> None:
            (home / "config.toml").write_text("\n".join([
                'model = "relay-resume-probe"',
                f'model_provider = "{provider}"',
                'cli_auth_credentials_store = "file"',
                '',
                f'[model_providers.{provider}]',
                f'name = "{provider}"',
                f'base_url = "{url}/v1"',
                'wire_api = "responses"',
                f'env_key = "{env_key}"',
                'request_max_retries = 0',
                'stream_max_retries = 0',
                '',
            ]), encoding="utf-8")

        config("legacy", f"http://127.0.0.1:{legacy_server.server_address[1]}", "LEGACY_PROBE_TOKEN")

        def seed(process: subprocess.Popen[str], output: queue.Queue[dict[str, Any]], timeout: float) -> None:
            send(process, {"id": 1, "method": "initialize", "params": {
                "clientInfo": {"name": "legacy-resume-probe", "version": "1"},
                "capabilities": {"experimentalApi": True},
            }})
            initialized = wait_for(output, lambda value: value.get("id") == 1, timeout)
            if initialized.get("error"):
                raise RuntimeError("legacy initialize failed")
            send(process, {"method": "initialized"})
            send(process, {"id": 2, "method": "thread/start", "params": {
                "cwd": str(workspace), "approvalPolicy": "never", "sandbox": "read-only",
            }})
            started = wait_for(output, lambda value: value.get("id") == 2, timeout)
            if started.get("error"):
                raise RuntimeError("legacy thread/start failed")
            thread_id = str(((started.get("result") or {}).get("thread") or {}).get("id") or "")
            if not thread_id:
                raise RuntimeError("legacy thread/start returned no thread ID")
            send(process, {"id": 3, "method": "turn/start", "params": {
                "threadId": thread_id,
                "input": [{"type": "text", "text": "Seed the persisted legacy rollout."}],
                "cwd": str(workspace), "approvalPolicy": "never",
            }})
            accepted = wait_for(output, lambda value: value.get("id") == 3, timeout)
            if accepted.get("error"):
                raise RuntimeError("legacy turn/start failed")
            completed = wait_for(output, lambda value: value.get("method") == "turn/completed", timeout)
            if str(((completed.get("params") or {}).get("turn") or {}).get("status") or "").lower() != "completed":
                raise RuntimeError("legacy seed turn did not complete")
            nonlocal seeded_thread_id
            seeded_thread_id = thread_id

        seeded_thread_id = ""
        run_process(executable, home, ["app-server", "--stdio"], seed, args.timeout,
                    {"LEGACY_PROBE_TOKEN": "local-legacy-probe-token"})
        config("relay_pool", f"http://127.0.0.1:{relay_server.server_address[1]}", "RELAY_PROBE_TOKEN")

        def resume(process: subprocess.Popen[str], output: queue.Queue[dict[str, Any]], timeout: float) -> None:
            send(process, {"id": 4, "method": "initialize", "params": {
                "clientInfo": {"name": "relay-resume-probe", "version": "1"},
                "capabilities": {"experimentalApi": True},
            }})
            initialized = wait_for(output, lambda value: value.get("id") == 4, timeout)
            if initialized.get("error"):
                raise RuntimeError("relay initialize failed")
            send(process, {"method": "initialized"})
            send(process, {"id": 5, "method": "thread/resume", "params": {
                "threadId": seeded_thread_id, "modelProvider": "relay_pool",
            }})
            resumed = wait_for(output, lambda value: value.get("id") == 5, timeout)
            if resumed.get("error"):
                raise RuntimeError("relay thread/resume failed: " + str(resumed["error"]))
            send(process, {"id": 6, "method": "turn/start", "params": {
                "threadId": seeded_thread_id,
                "input": [{"type": "text", "text": "Continue the resumed rollout."}],
                "cwd": str(workspace), "approvalPolicy": "never",
            }})
            accepted = wait_for(output, lambda value: value.get("id") == 6, timeout)
            if accepted.get("error"):
                raise RuntimeError("relay resumed turn/start failed")
            completed = wait_for(output, lambda value: value.get("method") == "turn/completed", timeout)
            if str(((completed.get("params") or {}).get("turn") or {}).get("status") or "").lower() != "completed":
                raise RuntimeError("relay resumed turn did not complete")

        relay_args = [
            "-c", "features.code_mode_host=true",
            "-c", 'model_provider="relay_pool"',
            "-c", 'model_providers.relay_pool.name="Codex Relay Pool"',
            "-c", f'model_providers.relay_pool.base_url="http://127.0.0.1:{relay_server.server_address[1]}/v1"',
            "-c", 'model_providers.relay_pool.wire_api="responses"',
            "-c", 'model_providers.relay_pool.env_key="RELAY_PROBE_TOKEN"',
            "-c", 'model_providers.relay_pool.requires_openai_auth=false',
            "-c", 'model_providers.relay_pool.request_max_retries=0',
            "-c", 'model_providers.relay_pool.stream_max_retries=0',
            "app-server", "-c", 'windows.sandbox="unelevated"', "--analytics-default-enabled",
        ]
        run_process(executable, home, relay_args, resume, args.timeout,
                    {"RELAY_PROBE_TOKEN": "local-relay-probe-token"})
        with legacy.lock, relay.lock:
            evidence = {
                "onePersistedThread": bool(seeded_thread_id),
                "threadId": seeded_thread_id,
                "legacyRequests": legacy.requests,
                "relayRequests": relay.requests,
                "resumedTurnUsedRelayProvider": bool(relay.requests),
            }
        print(json.dumps(evidence, indent=2))
        result = 0 if evidence["resumedTurnUsedRelayProvider"] and len(legacy.requests) == 1 else 1

    legacy_server.shutdown()
    relay_server.shutdown()
    legacy_server.server_close()
    relay_server.server_close()
    return result


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(json.dumps({"error": str(error)}))
        raise
