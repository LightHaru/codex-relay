#!/usr/bin/env python3
"""Run one real installed-Relay turn, native compact, and continuation turn."""

from __future__ import annotations

import argparse
import json
import os
import queue
import subprocess
import sys
import threading
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
    parser.add_argument("--cwd", required=True, type=Path)
    parser.add_argument("--checkpoint", default="RELAY_COMPACT_ALPHA_059")
    parser.add_argument("--timeout", type=float, default=120.0)
    parser.add_argument("--confirm-real-quota", action="store_true")
    return parser.parse_args()


def terminal_completed(message: dict[str, Any]) -> bool:
    params = message.get("params") or {}
    turn = params.get("turn") or {}
    return str(turn.get("status") or params.get("status") or "").lower() == "completed"


def contains_checkpoint(value: Any, checkpoint: str) -> bool:
    if isinstance(value, str):
        return checkpoint in value
    if isinstance(value, dict):
        return any(contains_checkpoint(item, checkpoint) for item in value.values())
    if isinstance(value, list):
        return any(contains_checkpoint(item, checkpoint) for item in value)
    return False


def main() -> int:
    args = arguments()
    if not args.confirm_real_quota:
        raise SystemExit("refusing real compact probe without --confirm-real-quota")
    executable = args.executable.resolve(strict=True)
    codex_home = args.codex_home.resolve(strict=True)
    cwd = args.cwd.resolve(strict=True)
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
    thread_id = ""
    try:
        send(process, {"id": 1, "method": "initialize", "params": {
            "clientInfo": {"name": "relay-compact-e2e", "title": "Relay compact E2E", "version": "0.5.9"},
            "capabilities": {"experimentalApi": True},
        }})
        initialized = wait_for(output, response_id_is(1), args.timeout, observed, process, diagnostics)
        require_successful_response("initialize failed", initialized)
        send(process, {"method": "initialized"})

        send(process, {"id": 2, "method": "thread/start", "params": {
            "cwd": str(cwd), "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": False,
        }})
        started = wait_for(output, response_id_is(2), args.timeout, observed, process, diagnostics)
        require_successful_response("thread start failed", started)
        thread_id = thread_id_from_response(started)
        if not thread_id:
            raise RuntimeError("thread/start returned no thread ID")

        send(process, {"id": 3, "method": "turn/start", "params": {
            "threadId": thread_id,
            "input": [{"type": "text", "text": f"Remember checkpoint {args.checkpoint}. Reply exactly ACK."}],
            "cwd": str(cwd), "approvalPolicy": "never",
        }})
        first_accepted = wait_for(output, response_id_is(3), args.timeout, observed, process, diagnostics)
        require_successful_response("first turn failed", first_accepted)
        first_terminal = wait_for_terminal(output, thread_id, args.timeout, observed)

        send(process, {"id": 4, "method": "thread/compact/start", "params": {"threadId": thread_id}})
        compact_accepted = wait_for(output, response_id_is(4), args.timeout, observed, process, diagnostics)
        require_successful_response("native compact failed", compact_accepted)
        compact_terminal = wait_for_terminal(output, thread_id, args.timeout, observed)

        post_start = len(observed)
        send(process, {"id": 5, "method": "turn/start", "params": {
            "threadId": thread_id,
            "input": [{"type": "text", "text": "Reply with exactly the checkpoint token from before compaction, and nothing else."}],
            "cwd": str(cwd), "approvalPolicy": "never",
        }})
        second_accepted = wait_for(output, response_id_is(5), args.timeout, observed, process, diagnostics)
        require_successful_response("post-compact turn failed", second_accepted)
        second_terminal = wait_for_terminal(output, thread_id, args.timeout, observed)
        post_messages = observed[post_start:]
        checkpoint_returned = any(contains_checkpoint(message, args.checkpoint) for message in post_messages)
        passed = all((
            terminal_completed(first_terminal),
            terminal_completed(compact_terminal),
            terminal_completed(second_terminal),
            checkpoint_returned,
        ))
        print(json.dumps({
            "threadCreated": bool(thread_id),
            "sameThreadAcrossCompact": True,
            "firstTurnCompleted": terminal_completed(first_terminal),
            "nativeCompactCompleted": terminal_completed(compact_terminal),
            "postCompactTurnCompleted": terminal_completed(second_terminal),
            "checkpointReturnedAfterCompact": checkpoint_returned,
            "postCompactEvidence": [
                {
                    "method": message.get("method"),
                    "id": message.get("id"),
                    "type": ((message.get("params") or {}).get("item") or {}).get("type") if isinstance((message.get("params") or {}).get("item"), dict) else None,
                    "text": str(((message.get("params") or {}).get("item") or {}).get("text", ""))[:240] if isinstance((message.get("params") or {}).get("item"), dict) else "",
                }
                for message in post_messages
                if message.get("method") in {"item/started", "item/completed", "turn/completed", "error"}
            ][-20:] if not checkpoint_returned else [],
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
