from __future__ import annotations

import argparse
import json
import os
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Literal, assert_never


type Command = Literal["list", "select"]
type OutputFormat = Literal["lines", "shell", "json"]
type ServiceMode = Literal["images", "tests", "pyprojects"]


@dataclass(frozen=True, slots=True)
class CliArgs:
    command: Command
    mode: ServiceMode
    output_format: OutputFormat
    changed_files: Path | None
    requested: str
    force_all: bool
    common_patterns: tuple[str, ...]


class CliError(Exception):
    pass


def main() -> int:
    try:
        args = _parse_args()
        repo_root = Path(__file__).resolve().parents[1]
        services = _apply_filters(
            _read_services(repo_root / "config" / "services.yml", args.mode),
            include=os.getenv("SERVICE_INCLUDE") or "",
            exclude=os.getenv("SERVICE_EXCLUDE") or "",
        )
        selected = _select_services(args, services)
        output = _format_services(selected, args.mode, args.output_format)
        try:
            stdout_buffer = sys.stdout.buffer
        except AttributeError:
            sys.stdout.write(output)
        else:
            stdout_buffer.write(output.encode("utf-8"))
    except CliError as exc:
        sys.stderr.write(f"{exc}\n")
        return 2
    return 0


def _parse_args() -> CliArgs:
    parser = argparse.ArgumentParser()
    parser.add_argument("command", nargs="?", default="list", choices=("list", "select"))
    parser.add_argument("--mode", choices=("images", "tests", "pyprojects"), default="images")
    parser.add_argument("--format", choices=("lines", "shell", "json"), default="lines")
    parser.add_argument("--changed-files", type=Path)
    parser.add_argument("--requested", default="")
    parser.add_argument("--all", action="store_true")
    parser.add_argument("--common", action="append", default=[])
    namespace = parser.parse_args()
    return CliArgs(
        command=namespace.command,
        mode=namespace.mode,
        output_format=namespace.format,
        changed_files=namespace.changed_files,
        requested=namespace.requested,
        force_all=namespace.all,
        common_patterns=tuple(namespace.common),
    )


def _read_services(config_path: Path, mode: ServiceMode) -> tuple[str, ...]:
    if not config_path.exists():
        return ()
    section = "tests" if mode == "pyprojects" else mode
    current_section = ""
    services: list[str] = []
    for raw_line in config_path.read_text(encoding="utf-8").splitlines():
        line = raw_line.split("#", maxsplit=1)[0].strip()
        if line == "":
            continue
        if line.endswith(":") and not line.startswith("-"):
            current_section = line[:-1].strip()
            continue
        if current_section == section and line.startswith("-"):
            item = line[1:].strip().strip("\"'")
            if item != "":
                services.append(item)
    return tuple(sorted(set(services)))


def _apply_filters(services: tuple[str, ...], *, include: str, exclude: str) -> tuple[str, ...]:
    selected = services
    include_words = _normalize_words(include)
    if include_words:
        selected = tuple(_normalize_service(word, services) for word in include_words)

    excluded = frozenset(_normalize_service(word, services, strict=False) for word in _normalize_words(exclude))
    return tuple(service for service in sorted(set(selected)) if service not in excluded)


def _select_services(args: CliArgs, services: tuple[str, ...]) -> tuple[str, ...]:
    if args.command == "list":
        return services
    if args.force_all or args.requested == "all":
        return services
    if args.requested:
        return (_normalize_service(args.requested, services),)
    return _select_changed_services(services, args.changed_files, args.common_patterns)


def _select_changed_services(
    services: tuple[str, ...],
    changed_files: Path | None,
    common_patterns: tuple[str, ...],
) -> tuple[str, ...]:
    if changed_files is None or not changed_files.exists():
        return services

    selected: set[str] = set()
    common_changed = False
    for raw_path in changed_files.read_text(encoding="utf-8").splitlines():
        path = raw_path.strip().replace("\\", "/")
        if path == "":
            continue
        if any(_path_matches(pattern, path) for pattern in common_patterns):
            common_changed = True
        candidate = _service_candidate_from_path(path)
        if candidate in services:
            selected.add(candidate)

    return services if common_changed else tuple(sorted(selected))


def _service_candidate_from_path(path: str) -> str:
    for prefix in ("services/", "contracts/services/"):
        if path.startswith(prefix):
            remainder = path.removeprefix(prefix)
            return remainder.split("/", maxsplit=1)[0]
    return ""


def _normalize_service(raw: str, services: tuple[str, ...], *, strict: bool = True) -> str:
    if raw in services:
        return raw
    service_name = f"{raw}-service"
    if service_name in services:
        return service_name
    if strict:
        raise CliError(f"unknown service: {raw}")
    return raw


def _normalize_words(raw: str) -> tuple[str, ...]:
    return tuple(word for word in raw.replace(",", " ").split() if word)


def _path_matches(pattern: str, path: str) -> bool:
    normalized = pattern.strip().replace("\\", "/")
    return path == normalized or path.startswith(f"{normalized}/")


def _format_services(services: tuple[str, ...], mode: ServiceMode, output_format: OutputFormat) -> str:
    values = tuple(f"services/{service}/pyproject.toml" for service in services) if mode == "pyprojects" else services
    match output_format:
        case "lines":
            return "".join(f"{service}\n" for service in values)
        case "shell":
            return f"{' '.join(values)}\n" if values else "\n"
        case "json":
            return f"{json.dumps(values, ensure_ascii=False)}\n"
        case unreachable:
            assert_never(unreachable)


if __name__ == "__main__":
    raise SystemExit(main())
