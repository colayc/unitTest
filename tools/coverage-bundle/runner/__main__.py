"""Offline gcovr entry point owned by Unit Test IDE."""

from __future__ import annotations

import importlib.metadata
import json
import os
import shutil
import subprocess
import sys
import tempfile
import zipfile
from pathlib import Path


def _materialize_application(destination: Path) -> None:
    archive = Path(sys.argv[0]).resolve()
    with zipfile.ZipFile(archive) as source:
        for info in source.infolist():
            name = info.filename
            parts = Path(name).parts
            if info.is_dir():
                continue
            if not parts or Path(name).is_absolute() or ".." in parts:
                raise ValueError("unsafe bundled application entry")
            target = destination.joinpath(*parts)
            target.parent.mkdir(parents=True, exist_ok=True)
            with source.open(info) as input_file, target.open("wb") as output_file:
                shutil.copyfileobj(input_file, output_file)


def _run_from(directory: Path) -> int:
    sys.path.insert(0, str(directory))
    if sys.argv[1:] == ["--self-check"]:
        import gcovr  # noqa: F401
        import lxml.etree  # noqa: F401
        import markupsafe  # noqa: F401

        print(json.dumps({
            "python": f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}",
            "gcovr": importlib.metadata.version("gcovr"),
        }, sort_keys=True))
        return 0
    if len(sys.argv) != 2:
        raise ValueError("expected one Service-owned descriptor path")
    from contract import gcovr_arguments, load_descriptor
    from gcovr.__main__ import main as gcovr_main

    return int(gcovr_main(gcovr_arguments(load_descriptor(sys.argv[1]))) or 0)


def main() -> int:
    try:
        materialized = Path(__file__).resolve()
        if os.environ.get("UNIT_TEST_IDE_GCOVR_MATERIALIZED") == "1" and materialized.is_file():
            return _run_from(materialized.parent)
        with tempfile.TemporaryDirectory(prefix="unit-test-ide-gcovr-") as temporary:
            directory = Path(temporary)
            _materialize_application(directory)
            environment = os.environ.copy()
            environment["UNIT_TEST_IDE_GCOVR_MATERIALIZED"] = "1"
            completed = subprocess.run(
                [sys.executable, str(directory / "__main__.py"), *sys.argv[1:]],
                check=False,
                env=environment,
                shell=False,
            )
            return completed.returncode
    except (OSError, ValueError, json.JSONDecodeError) as error:
        print(f"coverage runner rejected input: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
