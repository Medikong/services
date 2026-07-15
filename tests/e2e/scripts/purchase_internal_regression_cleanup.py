from __future__ import annotations

import subprocess
from collections.abc import Callable
from dataclasses import dataclass
from typing import Literal, assert_never

from purchase_internal_regression_support import Gate, RunnerConfig

type ResourceName = Literal["containers", "networks", "volumes", "images"]
type ProcessRunner = Callable[[tuple[str, ...]], subprocess.CompletedProcess[str]]
type OutputEmitter = Callable[[subprocess.CompletedProcess[str]], None]


class CleanupFailure(RuntimeError):
    """Report failure to remove runner-owned resources."""


@dataclass(frozen=True, slots=True)
class CleanupIO:
    run_process: ProcessRunner
    emit_output: OutputEmitter


def _resource_references(
    config: RunnerConfig,
    resource: ResourceName,
    projects: tuple[str, ...],
    image_prefix: str,
    io: CleanupIO,
) -> tuple[str, ...]:
    commands: list[tuple[str, ...]] = []
    for project in projects:
        label = f"label=com.docker.compose.project={project}"
        match resource:
            case "containers":
                commands.append(
                    (
                        str(config.docker_bin),
                        "ps",
                        "--all",
                        "--quiet",
                        "--filter",
                        label,
                    ),
                )
            case "networks" | "volumes":
                commands.append(
                    (
                        str(config.docker_bin),
                        resource.removesuffix("s"),
                        "ls",
                        "--quiet",
                        "--filter",
                        label,
                    ),
                )
            case "images":
                commands.append(
                    (
                        str(config.docker_bin),
                        "image",
                        "ls",
                        "--format",
                        "{{.Repository}}:{{.Tag}}",
                        "--filter",
                        label,
                    ),
                )
            case unreachable:
                assert_never(unreachable)
    if resource == "images":
        commands.append(
            (
                str(config.docker_bin),
                "image",
                "ls",
                "--format",
                "{{.Repository}}:{{.Tag}}",
                "--filter",
                f"reference={image_prefix}-*:local",
            ),
        )
    references: set[str] = set()
    for command in commands:
        try:
            result = io.run_process(command)
        except RuntimeError as exc:
            raise CleanupFailure(f"failed to list owned {resource}: {exc}") from exc
        io.emit_output(result)
        if result.returncode != 0:
            raise CleanupFailure(f"failed to list owned {resource}")
        references.update(
            line.strip()
            for line in result.stdout.splitlines()
            if line.strip() and line.strip() != "<none>:<none>"
        )
    return tuple(sorted(references))


def cleanup_resources(
    config: RunnerConfig,
    gates: tuple[Gate, ...],
    io: CleanupIO,
) -> None:
    projects = tuple(
        dict.fromkeys(
            project for gate in gates for project in gate.cleanup_projects
        ),
    )
    image_prefix = dict(gates[0].variables)["TEST_RUNNER_IMAGE_PREFIX"]
    removals: tuple[tuple[ResourceName, tuple[str, ...]], ...] = (
        ("containers", (str(config.docker_bin), "container", "rm", "--force")),
        ("networks", (str(config.docker_bin), "network", "rm")),
        ("volumes", (str(config.docker_bin), "volume", "rm", "--force")),
        ("images", (str(config.docker_bin), "image", "rm", "--force")),
    )
    for resource, remove_prefix in removals:
        references = _resource_references(config, resource, projects, image_prefix, io)
        if references:
            try:
                result = io.run_process((*remove_prefix, *references))
            except RuntimeError as exc:
                raise CleanupFailure(
                    f"failed to remove owned {resource}: {exc}",
                ) from exc
            io.emit_output(result)
            if result.returncode != 0:
                raise CleanupFailure(f"failed to remove owned {resource}")
    remaining = tuple(
        len(_resource_references(config, resource, projects, image_prefix, io))
        for resource, _remove_prefix in removals
    )
    print(
        "cleanup remaining "
        f"containers={remaining[0]} networks={remaining[1]} "
        f"volumes={remaining[2]} images={remaining[3]}",
        flush=True,
    )
    if any(remaining):
        raise CleanupFailure("runner-owned Docker resources remain after cleanup")
