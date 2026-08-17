"""Closed Service-to-gcovr runner contract."""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any

EXPECTED_FIELDS = {
    "schemaVersion",
    "root",
    "objectDirectory",
    "gcovExecutable",
    "outputPath",
}
PATH_FIELDS = ("root", "objectDirectory", "gcovExecutable", "outputPath")
MAX_DESCRIPTOR_BYTES = 64 * 1024


def _closed_object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise ValueError(f"duplicate descriptor field: {key}")
        value[key] = item
    return value


def load_descriptor(descriptor_path: str) -> dict[str, Any]:
    if not isinstance(descriptor_path, str) or not os.path.isabs(descriptor_path):
        raise ValueError("descriptor path must be absolute")
    path = Path(descriptor_path)
    if path.stat().st_size > MAX_DESCRIPTOR_BYTES:
        raise ValueError("descriptor is too large")
    value = json.loads(path.read_text(encoding="utf-8"), object_pairs_hook=_closed_object)
    if not isinstance(value, dict) or set(value.keys()) != EXPECTED_FIELDS:
        raise ValueError("descriptor fields do not match the closed contract")
    if value["schemaVersion"] != 1:
        raise ValueError("unsupported descriptor schemaVersion")
    for field in PATH_FIELDS:
        item = value[field]
        if not isinstance(item, str) or not item or "\x00" in item or not os.path.isabs(item):
            raise ValueError(f"{field} must be an absolute path")
        if os.path.normpath(item) != item:
            raise ValueError(f"{field} must be normalized")
    return value


def gcovr_arguments(descriptor: dict[str, Any]) -> list[str]:
    return [
        "--root",
        descriptor["root"],
        "--object-directory",
        descriptor["objectDirectory"],
        "--gcov-executable",
        descriptor["gcovExecutable"],
        "--json",
        descriptor["outputPath"],
        "--json-pretty",
    ]
