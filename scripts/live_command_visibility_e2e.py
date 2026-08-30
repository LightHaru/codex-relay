#!/usr/bin/env python3
"""Verify an installed Relay publishes one native command item with full text."""

from __future__ import annotations

import argparse
import json
import os
import queue
import subprocess
import sys
import tempfile
import threading
import uuid
from pathlib import Path
from typing import Any

from live_app_server_e2e import (
    reader,
    require_successful_response,
    response_id_is,
    send,
    stderr_reader,
    thread_id_from_response,
    wait_for,
    wait_for_terminal,
)
def arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", required=True, type=Path)
    parser.add_argument("--codex-home", required=True, type=Path)
    parser.add_argument("--timeout", type=float, default=150.0)
    parser.add_argument("--confirm-real-quota", action="store_true")
    return parser.parse_args()


def terminal_completed(message: dict[str, Any]) -> bool:
    params = message.get("params") or {}
    turn = params.get("turn") or {}
    return str(turn.get("status") or params.get("status") or "").lower() == "completed"


def contains_text(value: Any, needle: str) -> bool:
    if isinstance(value, str):
        return needle in value
    if isinstance(value, dict):
        return any(contains_text(child, needle) for child in value.values())
    if isinstance(value, list):
        return any(contains_text(child, needle) for child in value)
    return False


def command_evidence(messages: list[dict[str, Any]], command: str) -> dict[str, Any]:
    items = [
        (message.get("params") or {}).get("item")
        for message in messages
        if message.get("method") in {"item/started", "item/completed"}
        and isinstance((message.get("params") or {}).get("item"), dict)
    ]
    command_items = [
        item for item in items
        if "command" in str(item.get("type") or "").lower()
        or any(key in item for key in ("command", "cmd", "commands"))
    ]
    return {
        "nativeCommandItemCount": len(command_items),
        "actualCommandTextPresent": any(contains_text(item, command) for item in command_items),
        "itemTypes": sorted({str(item.get("type") or "") for item in items}),
    }


def marker_count(path: Path, marker: str) -> int:
    if not path.exists():
        return 0
    return sum(1 for line in path.read_text(encoding="utf-8").splitlines() if line.strip() == marker)


def main() -> int:
    args = arguments()
    if not args.confirm_real_quota:
        raise SystemExit("refusing real command probe without --confirm-real-quota")
    executable = args.executable.resolve(strict=True)
    codex_home = args.codex_home.resolve(strict=True)
    with tempfile.TemporaryDirectory(prefix="relay-command-visibility-") as temporary_name:
        workspace = Path(temporary_name)
        marker = "COMMAND_MARKER_" + uuid.uuid4().hex[:12].upper()
        marker_path = workspace / "command-marker.txt"
        command = f"Add-Content -LiteralPath '{marker_path}' -Value '{marker}'"
        environment = os.environ.copy()
        environment["CODEX_HOME"] = str(codex_home)
        environment["CODEX_SQLITE_HOME"] = str(codex_home)
        process = subprocess.Popen(
            [str(executable), "app-server", "--stdio"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            encoding="utf-8",
            errors="replace",
            bufsize=1,
            env=environment,
        )
        assert process.stdout is not None and process.stderr is not None
        output: queue.Queue[dict[str, Any]] = queue.Queue()
        diagnostics: queue.Queue[str] = queue.Queue()
        threading.Thread(target=reader, args=(process.stdout, output), daemon=True).start()
        threading.Thread(target=stderr_reader, args=(process.stderr, diagnostics), daemon=True).start()
        observed: list[dict[str, Any]] = []
        try:
            send(process, {"id": 1, "method": "initialize", "params": {
                "clientInfo": {"name": "relay-command-e2e", "title": "Relay command E2E", "version": "0.5.9"},
                "capabilities": {"experimentalApi": True},
            }})
            initialized = wait_for(output, response_id_is(1), args.timeout, observed, process, diagnostics)
            require_successful_response("initialize failed", initialized)
            send(process, {"method": "initialized"})
            send(process, {"id": 2, "method": "thread/start", "params": {
                "cwd": str(workspace), "approvalPolicy": "never", "sandbox": "danger-full-access", "ephemeral": False,
            }})
            started = wait_for(output, response_id_is(2), args.timeout, observed, process, diagnostics)
            require_successful_response("thread start failed", started)
            thread_id = thread_id_from_response(started)
            if not thread_id:
                raise RuntimeError("thread/start returned no thread ID")
            turn_start = len(observed)
            send(process, {"id": 3, "method": "turn/start", "params": {
                "threadId": thread_id,
                "input": [{"type": "text", "text": (
                    "This is an authorized command-visibility test. Use the shell tool now and run exactly this "
                    f"PowerShell command: {command}\nAfter it succeeds, reply exactly COMMAND_EXECUTION_OK."
                )}],
                "cwd": str(workspace), "approvalPolicy": "never",
            }})
            accepted = wait_for(output, response_id_is(3), args.timeout, observed, process, diagnostics)
            require_successful_response("command turn failed", accepted)
            terminal = wait_for_terminal(output, thread_id, args.timeout, observed)
            turn_messages = observed[turn_start:]
            evidence = command_evidence(turn_messages, command)
            count = marker_count(marker_path, marker)
            passed = bool(terminal_completed(terminal) and count == 1 and evidence["actualCommandTextPresent"])
            print(json.dumps({
                "turnCompleted": terminal_completed(terminal),
                "markerExecutionCount": count,
                "nativeCommandItemCount": evidence["nativeCommandItemCount"],
                "actualCommandTextPresent": evidence["actualCommandTextPresent"],
                "itemTypes": evidence["itemTypes"],
                "passed": passed,
            }, indent=2))
            return 0 if passed else 1
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


if __name__ == "__main__":
    sys.exit(main())
