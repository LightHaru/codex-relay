#!/usr/bin/env python3
"""Read-only, permission-gated quota probe for isolated Codex homes.

This starts one exact app-server child per operator-selected source home and
asks only for ``account/rateLimits/read``. It never prints auth data, account
identifiers, raw JSON, or source paths. The probe is intentionally opt-in and
does not belong to the normal release check because the service state can be
temporarily refreshed by the provider.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import queue
import threading
import time
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", required=True, type=Path)
    parser.add_argument("--source-dir", action="append", required=True, type=Path)
    parser.add_argument("--timeout", type=float, default=30.0)
    parser.add_argument(
        "--confirm-read-only-quota",
        action="store_true",
        help="acknowledge that the provider may refresh quota metadata",
    )
    return parser.parse_args()


def reader(stream: Any, output: queue.Queue[dict[str, Any]]) -> None:
    for line in iter(stream.readline, ""):
        line = line.strip()
        if not line:
            continue
        try:
            output.put(json.loads(line))
        except json.JSONDecodeError:
            continue


def send(process: subprocess.Popen[str], message: dict[str, Any]) -> None:
    assert process.stdin is not None
    process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
    process.stdin.flush()


def wait_response(output: queue.Queue[dict[str, Any]], request_id: int, timeout: float) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            message = output.get(timeout=min(0.5, max(0.01, deadline - time.monotonic())))
        except queue.Empty:
            continue
        if message.get("id") == request_id and "method" not in message:
            return message
    raise TimeoutError("timed out waiting for app-server quota response")


def percentage(window: Any) -> float | None:
    if not isinstance(window, dict):
        return None
    value = window.get("usedPercent")
    return float(value) if isinstance(value, (int, float)) else None


def reset(window: Any) -> int | None:
    if not isinstance(window, dict):
        return None
    value = window.get("resetsAt")
    return int(value) if isinstance(value, (int, float)) else None


def reached_type(value: Any) -> str | None:
    # Keep the report bounded to the two categories the router understands;
    # never echo arbitrary provider strings into an evidence file.
    normalized = str(value or "").strip().lower()
    if normalized in {"rate_limit_reached", "usage_limit_reached"}:
        return normalized
    return None


def one_source(executable: Path, source_dir: Path, timeout: float) -> dict[str, Any]:
    auth_path = source_dir / "auth.json"
    if not auth_path.is_file():
        return {"status": "missing_auth"}
    environment = os.environ.copy()
    environment["CODEX_HOME"] = str(source_dir.resolve(strict=True))
    environment["CODEX_SQLITE_HOME"] = str(source_dir.resolve(strict=True))
    process = subprocess.Popen(
        [str(executable.resolve(strict=True)), "app-server", "--stdio"],
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
    threading.Thread(target=reader, args=(process.stdout, output), daemon=True).start()
    try:
        send(process, {"id": 1, "method": "initialize", "params": {
            "clientInfo": {"name": "codex-relay-quota-probe", "title": "Codex Relay quota probe", "version": "0.5.0"},
            "capabilities": {"experimentalApi": True},
        }})
        initialized = wait_response(output, 1, timeout)
        if initialized.get("error") is not None:
            return {"status": "initialize_error"}
        send(process, {"method": "initialized"})
        send(process, {"id": 2, "method": "account/rateLimits/read", "params": {}})
        response = wait_response(output, 2, timeout)
        if response.get("error") is not None:
            return {"status": "quota_unavailable"}
        result = response.get("result") or {}
        limits = result.get("rateLimits") if isinstance(result, dict) else None
        if not isinstance(limits, dict):
            return {"status": "quota_unavailable"}
        primary = limits.get("primary")
        secondary = limits.get("secondary")
        return {
            "status": "ok",
            "primaryUsedPercent": percentage(primary),
            "primaryResetsAt": reset(primary),
            "secondaryUsedPercent": percentage(secondary),
            "secondaryResetsAt": reset(secondary),
            "rateLimitReachedType": reached_type(limits.get("rateLimitReachedType")),
        }
    except (OSError, TimeoutError):
        return {"status": "probe_error"}
    finally:
        if process.stdin is not None:
            process.stdin.close()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=3)


def main() -> int:
    args = parse_args()
    if not args.confirm_read_only_quota:
        raise SystemExit("refusing quota probe without --confirm-read-only-quota")
    executable = args.executable.resolve(strict=True)
    summaries = [one_source(executable, source, args.timeout) for source in args.source_dir]
    print(json.dumps({"sources": summaries}, indent=2))
    return 0 if all(summary.get("status") == "ok" for summary in summaries) else 1


if __name__ == "__main__":
    sys.exit(main())
