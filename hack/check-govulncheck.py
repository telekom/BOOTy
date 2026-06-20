#!/usr/bin/env python3
"""Run govulncheck and fail on non-allowlisted reachable vulnerabilities."""

from __future__ import annotations

import json
import subprocess
import sys
from dataclasses import dataclass
from typing import Any


GOVULNCHECK = [
    "go",
    "run",
    "golang.org/x/vuln/cmd/govulncheck@v1.1.4",
    "-format=json",
    "./...",
]


@dataclass(frozen=True)
class AllowlistedVulnerability:
    module: str
    reason: str


ALLOWLIST = {
    # GoBGP v3.37.0 is the latest published v3 module and the Go vulnerability
    # database currently reports no fixed version. Keep this narrow so the gate
    # still fails on any other reachable advisory or module.
    "GO-2026-4736": AllowlistedVulnerability(
        module="github.com/osrg/gobgp/v3",
        reason="latest upstream module, no fixed version reported",
    ),
}


def iter_json_objects(data: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    objects: list[dict[str, Any]] = []
    idx = 0
    while idx < len(data):
        while idx < len(data) and data[idx].isspace():
            idx += 1
        if idx >= len(data):
            break
        obj, end = decoder.raw_decode(data, idx)
        if not isinstance(obj, dict):
            raise ValueError("govulncheck emitted a non-object JSON value")
        objects.append(obj)
        idx = end
    return objects


def is_symbol_finding(finding: dict[str, Any]) -> bool:
    return any(
        isinstance(frame.get("function"), str) and frame["function"] != ""
        for frame in finding.get("trace", [])
        if isinstance(frame, dict)
    )


def finding_modules(finding: dict[str, Any]) -> set[str]:
    modules: set[str] = set()
    for frame in finding.get("trace", []):
        if not isinstance(frame, dict):
            continue
        module = frame.get("module")
        if isinstance(module, str):
            modules.add(module)
    return modules


def main() -> int:
    result = subprocess.run(
        GOVULNCHECK,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    if result.stderr:
        print(result.stderr, file=sys.stderr, end="")
    if result.returncode != 0:
        print("::error::govulncheck execution failed", file=sys.stderr)
        print(result.stdout, file=sys.stderr)
        return result.returncode

    try:
        objects = iter_json_objects(result.stdout)
    except ValueError as exc:
        print(f"::error::failed to parse govulncheck JSON: {exc}", file=sys.stderr)
        print(result.stdout, file=sys.stderr)
        return 1

    osv_by_id = {
        obj["osv"]["id"]: obj["osv"]
        for obj in objects
        if isinstance(obj.get("osv"), dict) and isinstance(obj["osv"].get("id"), str)
    }

    findings: dict[str, set[str]] = {}
    for obj in objects:
        finding = obj.get("finding")
        if not isinstance(finding, dict):
            continue
        osv_id = finding.get("osv")
        if not isinstance(osv_id, str):
            continue
        if not is_symbol_finding(finding):
            continue
        findings.setdefault(osv_id, set()).update(finding_modules(finding))

    unexpected: dict[str, set[str]] = {}
    allowed: dict[str, set[str]] = {}
    for osv_id, modules in findings.items():
        allow = ALLOWLIST.get(osv_id)
        if allow and allow.module in modules:
            allowed[osv_id] = modules
            continue
        unexpected[osv_id] = modules

    for osv_id, modules in sorted(allowed.items()):
        allow = ALLOWLIST[osv_id]
        summary = osv_by_id.get(osv_id, {}).get("summary", "allowed vulnerability")
        print(
            f"::warning::Allowed govulncheck finding {osv_id} "
            f"({summary}) for {allow.module}: {allow.reason}. "
            f"Modules in traces: {', '.join(sorted(modules))}"
        )

    if unexpected:
        for osv_id, modules in sorted(unexpected.items()):
            summary = osv_by_id.get(osv_id, {}).get("summary", "reachable vulnerability")
            print(
                f"::error::Unexpected govulncheck finding {osv_id} "
                f"({summary}). Modules in traces: {', '.join(sorted(modules))}",
                file=sys.stderr,
            )
        return 1

    if not findings:
        print("govulncheck found no reachable vulnerabilities")
    else:
        print("govulncheck found only documented allowlisted vulnerabilities")
    return 0


if __name__ == "__main__":
    sys.exit(main())
