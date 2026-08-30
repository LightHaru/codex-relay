#!/usr/bin/env python3
"""Permission-gated real app-server probe for installed Codex Relay.

The probe never reads credentials. It starts an already-installed Codex or
Relay app-server, sends one minimal text turn, and emits a sanitized summary.
It is intentionally excluded from `npm run check` because it consumes real
subscription quota and may create one persisted test thread.
"""

from __future__ import annotations

import argparse
import json
import os
import queue
import re
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", required=True, type=Path)
    parser.add_argument("--codex-home", type=Path)
    parser.add_argument("--cwd", required=True, type=Path)
    parser.add_argument("--prompt", required=True)
    parser.add_argument("--thread-id")
    parser.add_argument(
        "--goal-objective",
        help="optional short objective to set before the turn and verify afterwards",
    )
    parser.add_argument(
        "--goal-token-budget",
        type=int,
        help="optional finite token budget for a newly set Goal",
    )
    parser.add_argument(
        "--verify-goal-objective",
        help="verify an existing goal after the turn without setting it first",
    )
    parser.add_argument("--timeout", type=float, default=90.0)
    parser.add_argument(
        "--confirm-real-quota",
        action="store_true",
        help="required acknowledgement that this sends one real model turn",
    )
    parser.add_argument(
        "--dump-errors",
        action="store_true",
        help="include bounded app-server error/terminal details in the sanitized probe output",
    )
    parser.add_argument(
        "--dump-stderr",
        action="store_true",
        help="include bounded child stderr diagnostics in the probe output",
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
            output.put({"_nonJson": line[:200]})


def stderr_reader(stream: Any, output: queue.Queue[str]) -> None:
    for line in iter(stream.readline, ""):
        line = line.strip()
        if line:
            output.put(line[:1000])


def send(process: subprocess.Popen[str], message: dict[str, Any]) -> None:
    assert process.stdin is not None
    process.stdin.write(json.dumps(message, separators=(",", ":")) + "\n")
    process.stdin.flush()


def wait_for(
    output: queue.Queue[dict[str, Any]],
    predicate: Any,
    timeout: float,
    observed: list[dict[str, Any]],
    process: subprocess.Popen[str] | None = None,
    diagnostics: queue.Queue[str] | None = None,
) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            message = output.get(timeout=min(0.5, max(0.01, deadline - time.monotonic())))
        except queue.Empty:
            if process is not None and process.poll() is not None:
                lines = list(diagnostics.queue)[-4:] if diagnostics is not None else []
                detail = " | ".join(lines)
                detail = re.sub(r"(?i)bearer\s+[^\s\"']+", "Bearer [redacted]", detail)
                raise RuntimeError(
                    f"app-server exited before response: code={process.returncode} "
                    f"diagnostic={detail[:600]}"
                )
            continue
        observed.append(message)
        if predicate(message):
            return message
    raise TimeoutError("timed out waiting for an app-server response")


def response_id_is(expected: int):
    return lambda message: message.get("id") == expected and "method" not in message


def thread_id_from_response(message: dict[str, Any]) -> str:
    result = message.get("result") or {}
    thread = result.get("thread") or {}
    return str(thread.get("id") or result.get("threadId") or result.get("thread_id") or "")


def notification_thread_id(message: dict[str, Any]) -> str:
    params = message.get("params") or {}
    return str(
        params.get("threadId")
        or params.get("thread_id")
        or ((params.get("turn") or {}).get("threadId"))
        or ""
    )


def wait_for_terminal(
    output: queue.Queue[dict[str, Any]],
    thread_id: str,
    timeout: float,
    observed: list[dict[str, Any]],
) -> dict[str, Any]:
    deadline = time.monotonic() + timeout
    first_error: dict[str, Any] | None = None
    error_at = 0.0
    while time.monotonic() < deadline:
        if first_error is not None and time.monotonic() - error_at >= 10.0:
            return first_error
        try:
            message = output.get(timeout=min(0.5, max(0.01, deadline - time.monotonic())))
        except queue.Empty:
            continue
        observed.append(message)
        if notification_thread_id(message) != thread_id:
            continue
        if message.get("method") == "error" and first_error is None:
            # app-server can emit an error before the authoritative failed
            # turn/completed event. During a handoff, a late source-generation
            # error can also precede a successful target completion. Keep it as
            # a bounded fallback instead of terminating the probe immediately.
            first_error = message
            error_at = time.monotonic()
            continue
        if message.get("method") != "turn/completed":
            continue
        params = message.get("params") or {}
        turn = params.get("turn") or {}
        if str(turn.get("status") or params.get("status") or "").lower() in {
            "completed",
            "failed",
            "cancelled",
            "interrupted",
        }:
            return message
    if first_error is not None:
        return first_error
    raise TimeoutError("timed out waiting for a terminal turn notification")


def sanitized_error(message: dict[str, Any]) -> str:
    value: Any = message.get("error")
    if value is None:
        params = message.get("params") or {}
        value = params.get("error") or ((params.get("turn") or {}).get("error"))
    if isinstance(value, dict):
        value = value.get("message") or value.get("codexErrorInfo") or value.get("code")
    text = str(value or "")
    lowered = text.lower()
    if "usage" in lowered or "rate limit" in lowered or "quota" in lowered:
        return "quota_exhausted"
    if "capacity" in lowered:
        return "model_capacity"
    if "stream disconnected" in lowered or "stream closed" in lowered:
        return "stream_disconnected"
    if "409" in lowered or "already active" in lowered or "logical_turn" in lowered:
        return "logical_turn_conflict"
    return "none" if not text else "other"


def sanitized_error_code(message: dict[str, Any]) -> str:
    """Return a bounded category/code without exposing provider payloads."""
    value: Any = message.get("error")
    if value is None:
        params = message.get("params") or {}
        value = params.get("error") or ((params.get("turn") or {}).get("error"))
    candidates: list[str] = []

    def collect(item: Any) -> None:
        if isinstance(item, dict):
            for key in ("code", "type", "name"):
                candidate = item.get(key)
                if isinstance(candidate, (str, int, float)):
                    candidates.append(str(candidate))
            for key in ("error", "cause", "codexErrorInfo"):
                if key in item:
                    collect(item[key])
        elif isinstance(item, (str, int, float)):
            candidates.append(str(item))

    collect(value)
    for candidate in candidates:
        lowered = candidate.lower().strip()
        if not lowered:
            continue
        if "usage" in lowered or "quota" in lowered or "rate_limit" in lowered:
            return "quota_exhausted"
        if "stream" in lowered and ("disconnect" in lowered or "closed" in lowered):
            return "stream_disconnected"
        if "logical_turn" in lowered or "already_active" in lowered or "409" in lowered:
            return "logical_turn_conflict"
        if all(ch.isalnum() or ch in "._-" for ch in candidate) and len(candidate) <= 96:
            return candidate
    return sanitized_error(message)


def require_successful_response(label: str, message: dict[str, Any]) -> None:
    if message.get("error") is None:
        return
    value: Any = message.get("error")
    if isinstance(value, dict):
        value = value.get("message") or value.get("code") or ""
    detail = re.sub(r"(?i)bearer\s+[^\s\"']+", "Bearer [redacted]", str(value or ""))
    detail = " ".join(detail.split())[:300]
    raise RuntimeError(
        f"{label}: category={sanitized_error(message)} "
        f"code={sanitized_error_code(message)} message={detail}"
    )


def main() -> int:
    args = parse_args()
    if not args.confirm_real_quota:
        raise SystemExit("refusing real turn without --confirm-real-quota")
    if args.goal_objective and args.verify_goal_objective:
        raise SystemExit("choose either --goal-objective or --verify-goal-objective")
    if args.goal_token_budget is not None and not args.goal_objective:
        raise SystemExit("--goal-token-budget requires --goal-objective")
    if args.goal_token_budget is not None and args.goal_token_budget <= 0:
        raise SystemExit("--goal-token-budget must be positive")
    executable = args.executable.resolve(strict=True)
    cwd = args.cwd.resolve(strict=True)
    environment = os.environ.copy()
    if args.codex_home is not None:
        home = args.codex_home.resolve(strict=True)
        environment["CODEX_HOME"] = str(home)
        environment["CODEX_SQLITE_HOME"] = str(home)

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
    errors: queue.Queue[str] = queue.Queue()
    threading.Thread(target=reader, args=(process.stdout, output), daemon=True).start()
    threading.Thread(target=stderr_reader, args=(process.stderr, errors), daemon=True).start()
    observed: list[dict[str, Any]] = []
    thread_id = args.thread_id or ""
    try:
        send(process, {
            "id": 1,
            "method": "initialize",
            "params": {
                "clientInfo": {"name": "codex-relay-e2e", "title": "Codex Relay E2E", "version": "0.5.9"},
                "capabilities": {"experimentalApi": True},
            },
        })
        initialized = wait_for(output, response_id_is(1), args.timeout, observed, process, errors)
        require_successful_response("initialize failed", initialized)
        send(process, {"method": "initialized"})

        if thread_id:
            send(process, {
                "id": 2,
                "method": "thread/resume",
                "params": {"threadId": thread_id, "cwd": str(cwd), "approvalPolicy": "never", "sandbox": "read-only"},
            })
            resumed = wait_for(output, response_id_is(2), args.timeout, observed, process, errors)
            require_successful_response("thread resume failed", resumed)
        else:
            send(process, {
                "id": 2,
                "method": "thread/start",
                "params": {"cwd": str(cwd), "approvalPolicy": "never", "sandbox": "read-only", "ephemeral": False},
            })
            started = wait_for(output, response_id_is(2), args.timeout, observed, process, errors)
            require_successful_response("thread start failed", started)
            thread_id = thread_id_from_response(started)
            if not thread_id:
                raise RuntimeError("thread/start returned no thread ID")

        next_id = 3
        goal_set_ok: bool | None = None
        if args.goal_objective:
            send(process, {
                "id": next_id,
                "method": "thread/goal/set",
                "params": {
                    "threadId": thread_id,
                    "objective": args.goal_objective,
                    "status": "active",
                    "tokenBudget": args.goal_token_budget,
                },
            })
            goal_set = wait_for(output, response_id_is(next_id), args.timeout, observed, process, errors)
            goal_set_ok = goal_set.get("error") is None
            if not goal_set_ok:
                require_successful_response("thread goal set failed", goal_set)
            next_id += 1

        turn_request_id = next_id
        send(process, {
            "id": turn_request_id,
            "method": "turn/start",
            "params": {
                "threadId": thread_id,
                "input": [{"type": "text", "text": args.prompt}],
                "cwd": str(cwd),
                "approvalPolicy": "never",
            },
        })
        turn_response = wait_for(output, response_id_is(turn_request_id), args.timeout, observed, process, errors)
        terminal = turn_response if turn_response.get("error") is not None else wait_for_terminal(
            output, thread_id, args.timeout, observed
        )
        terminal_status = ""
        if isinstance(terminal.get("params"), dict):
            terminal_params = terminal["params"]
            terminal_turn = terminal_params.get("turn") or {}
            terminal_status = str(
                terminal_turn.get("status") or terminal_params.get("status") or ""
            ).lower()
        expected_goal_objective = args.goal_objective or args.verify_goal_objective
        goal_present_after: bool | None = None
        goal_status_after: str | None = None
        goal_objective_matches: bool | None = None
        if expected_goal_objective:
            next_id += 1
            send(process, {
                "id": next_id,
                "method": "thread/goal/get",
                "params": {"threadId": thread_id},
            })
            goal_get = wait_for(output, response_id_is(next_id), args.timeout, observed, process, errors)
            goal = (goal_get.get("result") or {}).get("goal")
            goal_present_after = isinstance(goal, dict)
            if goal_present_after:
                goal_status_after = str(goal.get("status") or "")
                goal_objective_matches = goal.get("objective") == expected_goal_objective
        methods = [str(message.get("method")) for message in observed if message.get("method")]
        item_types = sorted({
            str(((message.get("params") or {}).get("item") or {}).get("type"))
            for message in observed
            if str(message.get("method") or "").startswith("item/")
        })
        # Give the asynchronous stderr reader a bounded opportunity to receive
        # startup/model-refresh diagnostics emitted beside turn completion.
        time.sleep(2.0 if args.dump_stderr else 0.25)
        stderr_lines = list(errors.queue)
        model_catalog_errors = [
            line
            for line in stderr_lines
            if "failed to refresh available models" in line.lower()
        ]
        goal_ok = True
        if expected_goal_objective:
            goal_ok = bool(goal_present_after and goal_objective_matches)
            if args.goal_objective:
                goal_ok = bool(goal_ok and goal_set_ok)
        passed = bool(
            turn_response.get("error") is None
            and terminal_status == "completed"
            and not model_catalog_errors
            and goal_ok
        )
        result = {
            "threadId": thread_id,
            "turnAccepted": turn_response.get("error") is None,
            "terminalMethod": terminal.get("method") or "response-error",
            "terminalStatus": terminal_status or ("error" if terminal.get("error") else "unknown"),
            "turnCompleted": terminal_status == "completed",
            "terminalErrorCategory": sanitized_error(terminal if terminal.get("method") else turn_response),
            "terminalErrorCode": sanitized_error_code(terminal if terminal.get("method") else turn_response),
            "goalSet": goal_set_ok,
            "goalPresentAfter": goal_present_after,
            "goalStatusAfter": goal_status_after,
            "goalObjectiveMatches": goal_objective_matches,
            "observedMethods": sorted(set(methods)),
            "observedItemTypes": item_types,
            "modelCatalogRefreshErrors": len(model_catalog_errors),
            "stderrLineCount": len(stderr_lines),
            "passed": passed,
        }
        if args.dump_errors:
            result["errorMessages"] = [
                {
                    "method": message.get("method"),
                    "id": message.get("id"),
                    "category": sanitized_error(message),
                    "code": sanitized_error_code(message),
                    "status": str(
                        ((message.get("params") or {}).get("turn") or {}).get("status")
                        or (message.get("params") or {}).get("status")
                        or ""
                    ),
                    "turnId": str(
                        ((message.get("params") or {}).get("turn") or {}).get("id")
                        or (message.get("params") or {}).get("turnId")
                        or ""
                    ),
                }
                for message in observed
                if message.get("method") in {"error", "turn/completed"}
            ]
        if args.dump_stderr:
            result["stderr"] = stderr_lines
        print(json.dumps(result, indent=2))
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
