#!/usr/bin/env python3
"""Probe one real Codex app-server against a local fake Responses provider.

This is a protocol test, not a quota test. It uses a temporary CODEX_HOME,
contains no user credentials, and never contacts the OpenAI service. Success
proves that one app-server can remain the task/tool authority while Relay owns
the model HTTP transport selected through a custom provider.
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import subprocess
import sys
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
    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.requests: list[dict[str, Any]] = []

    def append(self, request: dict[str, Any]) -> None:
        with self.lock:
            self.requests.append(request)


def response_payload(response_id: str, output: list[dict[str, Any]], status: str) -> dict[str, Any]:
    return {
        "id": response_id,
        "object": "response",
        "created_at": int(time.time()),
        "status": status,
        "error": None,
        "incomplete_details": None,
        "instructions": None,
        "max_output_tokens": None,
        "model": "relay-probe-model",
        "output": output,
        "parallel_tool_calls": True,
        "previous_response_id": None,
        "reasoning": {"effort": "medium", "summary": None},
        "store": False,
        "temperature": None,
        "text": {"format": {"type": "text"}},
        "tool_choice": "auto",
        "tools": [],
        "top_p": None,
        "truncation": "disabled",
        "usage": {
            "input_tokens": 1,
            "input_tokens_details": {"cached_tokens": 0},
            "output_tokens": 1,
            "output_tokens_details": {"reasoning_tokens": 0},
            "total_tokens": 2,
        },
        "user": None,
        "metadata": {},
    }


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
            state.append({
                "path": self.path,
                "stream": bool(body.get("stream")),
                "model": str(body.get("model") or ""),
                "hasAuthorization": bool(self.headers.get("Authorization")),
                "inputItemCount": len(body.get("input") or []),
                "bodyKeys": sorted(str(key) for key in body),
                "headerNames": sorted(
                    key.lower() for key in self.headers
                    if key.lower() not in {"authorization", "cookie"}
                ),
            })

            response_id = "resp_" + uuid.uuid4().hex
            item_id = "msg_" + uuid.uuid4().hex
            text = "RELAY_PROVIDER_OK"
            item = {
                "id": item_id,
                "type": "message",
                "status": "completed",
                "role": "assistant",
                "content": [{"type": "output_text", "text": text, "annotations": []}],
            }
            events = [
                ("response.created", {"type": "response.created", "sequence_number": 0,
                    "response": response_payload(response_id, [], "in_progress")}),
                ("response.output_item.added", {"type": "response.output_item.added", "sequence_number": 1,
                    "output_index": 0, "item": {**item, "status": "in_progress", "content": []}}),
                ("response.content_part.added", {"type": "response.content_part.added", "sequence_number": 2,
                    "item_id": item_id, "output_index": 0, "content_index": 0,
                    "part": {"type": "output_text", "text": "", "annotations": []}}),
                ("response.output_text.delta", {"type": "response.output_text.delta", "sequence_number": 3,
                    "item_id": item_id, "output_index": 0, "content_index": 0,
                    "delta": text, "logprobs": []}),
                ("response.output_text.done", {"type": "response.output_text.done", "sequence_number": 4,
                    "item_id": item_id, "output_index": 0, "content_index": 0,
                    "text": text, "logprobs": []}),
                ("response.content_part.done", {"type": "response.content_part.done", "sequence_number": 5,
                    "item_id": item_id, "output_index": 0, "content_index": 0,
                    "part": item["content"][0]}),
                ("response.output_item.done", {"type": "response.output_item.done", "sequence_number": 6,
                    "output_index": 0, "item": item}),
                ("response.completed", {"type": "response.completed", "sequence_number": 7,
                    "response": response_payload(response_id, [item], "completed")}),
            ]
            encoded = "".join(
                f"event: {name}\ndata: {json.dumps(payload, separators=(',', ':'))}\n\n"
                for name, payload in events
            ).encode("utf-8")
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


def main() -> int:
    args = parse_args()
    executable = args.executable.resolve(strict=True)
    state = ProviderState()
    server = ThreadingHTTPServer(("127.0.0.1", 0), handler_for(state))
    threading.Thread(target=server.serve_forever, daemon=True).start()

    with tempfile.TemporaryDirectory(prefix="codex-relay-provider-probe-") as directory:
        root = Path(directory)
        codex_home = root / "codex-home"
        workspace = root / "workspace"
        codex_home.mkdir()
        workspace.mkdir()
        base_url = f"http://127.0.0.1:{server.server_address[1]}/v1"
        (codex_home / "config.toml").write_text(
            "\n".join([
                'model = "relay-probe-model"',
                'model_provider = "relay_pool"',
                'cli_auth_credentials_store = "file"',
                '',
                '[model_providers.relay_pool]',
                'name = "Codex Relay Pool"',
                f'base_url = "{base_url}"',
                'wire_api = "responses"',
                'env_key = "CODEX_RELAY_PROBE_TOKEN"',
                'request_max_retries = 0',
                'stream_max_retries = 0',
                '',
            ]),
            encoding="utf-8",
        )
        environment = os.environ.copy()
        environment["CODEX_HOME"] = str(codex_home)
        environment["CODEX_SQLITE_HOME"] = str(codex_home)
        environment["CODEX_RELAY_PROBE_TOKEN"] = "sanitized-local-probe-token"
        process = subprocess.Popen(
            [str(executable), "app-server", "--stdio"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, encoding="utf-8", errors="replace", bufsize=1, env=environment,
        )
        assert process.stdout is not None
        output: queue.Queue[dict[str, Any]] = queue.Queue()
        threading.Thread(target=reader, args=(process.stdout, output), daemon=True).start()
        try:
            send(process, {"id": 1, "method": "initialize", "params": {
                "clientInfo": {"name": "relay-provider-probe", "title": "Relay provider probe", "version": "1"},
                "capabilities": {"experimentalApi": True},
            }})
            initialized = wait_for(output, lambda value: value.get("id") == 1, args.timeout)
            if initialized.get("error") is not None:
                raise RuntimeError("initialize failed")
            send(process, {"method": "initialized"})
            send(process, {"id": 2, "method": "thread/start", "params": {
                "cwd": str(workspace), "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": True,
            }})
            started = wait_for(output, lambda value: value.get("id") == 2, args.timeout)
            if started.get("error") is not None:
                raise RuntimeError("thread/start failed")
            result = started.get("result") or {}
            thread_id = str((result.get("thread") or {}).get("id") or result.get("threadId") or "")
            if not thread_id:
                raise RuntimeError("thread/start returned no thread ID")
            send(process, {"id": 3, "method": "turn/start", "params": {
                "threadId": thread_id,
                "input": [{"type": "text", "text": "Return the deterministic probe token."}],
                "cwd": str(workspace), "approvalPolicy": "never",
            }})
            accepted = wait_for(output, lambda value: value.get("id") == 3, args.timeout)
            if accepted.get("error") is not None:
                raise RuntimeError("turn/start failed")
            completed = wait_for(
                output,
                lambda value: value.get("method") == "turn/completed" and
                    str(((value.get("params") or {}).get("turn") or {}).get("status") or "").lower() in {"completed", "failed"},
                args.timeout,
            )
            status = str(((completed.get("params") or {}).get("turn") or {}).get("status") or "")
            evidence = {
                "oneAppServer": True,
                "oneThread": bool(thread_id),
                "turnAccepted": True,
                "turnStatus": status,
                "providerRequestCount": len(state.requests),
                "providerRequests": state.requests,
                "temporaryCodexHome": True,
                "userCredentialsRead": False,
            }
            print(json.dumps(evidence, indent=2))
            return 0 if status.lower() == "completed" and state.requests else 1
        finally:
            if process.stdin is not None:
                process.stdin.close()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    sys.exit(main())
