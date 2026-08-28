#!/usr/bin/env python3
"""Read one Codex thread and report whether native activity details survived.

This diagnostic is read-only and never prints commands, outputs, prompts, or
credentials.  It exists to catch compatibility regressions where a desktop
renderer receives generic ``Ran command`` rows because ``commandExecution``
items no longer contain their native ``command`` field.
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import subprocess
import threading
import time
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", required=True, type=Path)
    parser.add_argument("--codex-home", required=True, type=Path)
    parser.add_argument("--thread-id", required=True)
    parser.add_argument("--timeout", type=float, default=30.0)
    return parser.parse_args()


def read_messages(stream: Any, output: queue.Queue[dict[str, Any]]) -> None:
    for line in iter(stream.readline, ""):
        try:
            output.put(json.loads(line))
        except json.JSONDecodeError:
            continue


def send(process: subprocess.Popen[str], message: dict[str, Any]) -> None:
    assert process.stdin is not None
    process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
    process.stdin.flush()


def wait_response(
    process: subprocess.Popen[str],
    output: queue.Queue[dict[str, Any]],
    request_id: int,
    timeout: float,
) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            message = output.get(timeout=min(0.5, deadline - time.monotonic()))
        except queue.Empty:
            if process.poll() is not None:
                raise RuntimeError(f"app-server exited with code {process.returncode}")
            continue
        if message.get("id") == request_id and "method" not in message:
            return message
    raise TimeoutError(f"timed out waiting for response {request_id}")


def collect_items(value: Any, items: list[dict[str, Any]]) -> None:
    if isinstance(value, dict):
        if isinstance(value.get("type"), str):
            items.append(value)
        for nested in value.values():
            collect_items(nested, items)
    elif isinstance(value, list):
        for nested in value:
            collect_items(nested, items)


def main() -> int:
    args = parse_args()
    executable = args.executable.resolve(strict=True)
    codex_home = args.codex_home.resolve(strict=True)
    environment = os.environ.copy()
    environment["CODEX_HOME"] = str(codex_home)
    environment["CODEX_SQLITE_HOME"] = str(codex_home)
    process = subprocess.Popen(
        [str(executable), "app-server", "--stdio"],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.DEVNULL,
        text=True,
        encoding="utf-8",
        errors="replace",
        bufsize=1,
        env=environment,
    )
    assert process.stdout is not None
    output: queue.Queue[dict[str, Any]] = queue.Queue()
    threading.Thread(target=read_messages, args=(process.stdout, output), daemon=True).start()
    try:
        send(
            process,
            {
                "id": 1,
                "method": "initialize",
                "params": {
                    "clientInfo": {
                        "name": "codex-relay-activity-probe",
                        "title": "Codex Relay Activity Probe",
                        "version": "1",
                    },
                    "capabilities": {"experimentalApi": True},
                },
            },
        )
        initialized = wait_response(process, output, 1, args.timeout)
        if initialized.get("error") is not None:
            raise RuntimeError("initialize failed")
        send(process, {"method": "initialized"})
        send(
            process,
            {
                "id": 2,
                "method": "thread/read",
                "params": {"threadId": args.thread_id, "includeTurns": True},
            },
        )
        response = wait_response(process, output, 2, args.timeout)
        if response.get("error") is not None:
            raise RuntimeError("thread/read failed")
        items: list[dict[str, Any]] = []
        collect_items(response.get("result"), items)
        commands = [item for item in items if item.get("type") == "commandExecution"]
        details = [
            item
            for item in commands
            if isinstance(item.get("command"), str) and item["command"].strip()
        ]
        outputs = [
            item
            for item in commands
            if isinstance(item.get("aggregatedOutput"), str)
        ]
        type_counts: dict[str, int] = {}
        key_sets: dict[str, set[str]] = {}
        mcp_tool_counts: dict[str, int] = {}
        mcp_argument_shapes: dict[str, list[str]] = {}
        titled_js_calls = 0
        generic_js_titles = 0
        for item in items:
            item_type = str(item.get("type"))
            type_counts[item_type] = type_counts.get(item_type, 0) + 1
            key_sets.setdefault(item_type, set()).update(str(key) for key in item)
            if item_type == "mcpToolCall":
                tool_name = str(item.get("tool") or "unknown")
                mcp_tool_counts[tool_name] = mcp_tool_counts.get(tool_name, 0) + 1
                arguments = item.get("arguments")
                if isinstance(arguments, dict):
                    mcp_argument_shapes.setdefault(tool_name, [])
                    mcp_argument_shapes[tool_name] = sorted(
                        set(mcp_argument_shapes[tool_name]) | {str(key) for key in arguments}
                    )
                    if tool_name == "js" and str(arguments.get("title") or "").strip():
                        titled_js_calls += 1
                        if str(arguments.get("title") or "").strip().lower() in {
                            "ran command",
                            "running command",
                            "run command",
                        }:
                            generic_js_titles += 1
        print(
            json.dumps(
                {
                    "threadFound": True,
                    "commandExecutionCount": len(commands),
                    "commandDetailCount": len(details),
                    "commandOutputCount": len(outputs),
                    "allCommandsHaveDetails": bool(commands) and len(details) == len(commands),
                    "itemTypeCounts": dict(sorted(type_counts.items())),
                    "itemKeys": {
                        item_type: sorted(keys)
                        for item_type, keys in sorted(key_sets.items())
                    },
                    "mcpToolCounts": dict(sorted(mcp_tool_counts.items())),
                    "mcpArgumentKeys": dict(sorted(mcp_argument_shapes.items())),
                    "titledJsCallCount": titled_js_calls,
                    "genericJsTitleCount": generic_js_titles,
                    "nativeActivityDetailHealthy": bool(commands or mcp_tool_counts.get("js", 0))
                    and len(details) == len(commands)
                    and titled_js_calls == mcp_tool_counts.get("js", 0)
                    and generic_js_titles == 0,
                },
                indent=2,
            )
        )
        native_activity_detail_healthy = bool(commands or mcp_tool_counts.get("js", 0))
        native_activity_detail_healthy = bool(
            native_activity_detail_healthy
            and len(details) == len(commands)
            and titled_js_calls == mcp_tool_counts.get("js", 0)
            and generic_js_titles == 0
        )
        return 0 if native_activity_detail_healthy else 1
    finally:
        if process.stdin is not None:
            process.stdin.close()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.terminate()
            process.wait(timeout=5)


if __name__ == "__main__":
    raise SystemExit(main())
