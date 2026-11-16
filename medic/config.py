"""TOML config loader: declare checks (with optional stages) in a file.

Example::

    [settings]
    timeout = 6
    fail_on = "warning"        # or "failure" (default)

    [[check]]
    type = "tcp"
    target = "db:5432"
    stage = 0                  # later stages are skipped if an earlier one fails

    [[check]]
    type = "http"
    name = "API health"
    url = "https://api.example.com/health"
    expect_status = [200]
    stage = 1
"""

import sys
from pathlib import Path
from typing import Any, Callable, Dict, List, Optional, Tuple

if sys.version_info >= (3, 11):
    import tomllib
else:  # pragma: no cover
    import tomli as tomllib

from medic.checks.dns import DNSCheck
from medic.checks.exec import ExecCheck
from medic.checks.http import HTTPCheck
from medic.checks.ping import PingCheck
from medic.checks.ssh import SSHCheck
from medic.checks.system import SystemCheckGroup
from medic.checks.tcp import TCPCheck
from medic.checks.tls import TLSCheck
from medic.core.base import BaseCheck

SETTINGS_KEYS = {"timeout", "concurrency", "fail_on", "stop_on_failure", "parallel"}


class ConfigError(ValueError):
    """Raised for invalid config files."""


def _pair(value: Any, key: str) -> Tuple[float, float]:
    if not (isinstance(value, (list, tuple)) and len(value) == 2):
        raise ConfigError(f"'{key}' must be [warn, crit]")
    return float(value[0]), float(value[1])


def _status_list(value: Any) -> List[int]:
    if isinstance(value, int):
        return [value]
    if isinstance(value, list) and all(isinstance(v, int) for v in value):
        return list(value)
    raise ConfigError("'expect_status' must be an int or a list of ints")


def _build_system(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    cpu = _pair(o.pop("cpu", [80, 95]), "cpu")
    ram = _pair(o.pop("ram", [85, 95]), "ram")
    disk = _pair(o.pop("disk_threshold", [85, 92]), "disk_threshold")
    return SystemCheckGroup(
        disk_paths=list(o.pop("disks", ["/"])),
        cpu_warn=cpu[0], cpu_crit=cpu[1],
        ram_warn=ram[0], ram_crit=ram[1],
        disk_warn=disk[0], disk_crit=disk[1],
        timeout=timeout,
    )


def _build_ping(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    return PingCheck(host=o.pop("host"), name=base["name"], count=int(o.pop("count", 3)), timeout_sec=timeout)


def _build_tcp(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    if "target" in o:
        return TCPCheck.from_target(o.pop("target"), name=base["name"], timeout=timeout)
    return TCPCheck(host=o.pop("host"), port=int(o.pop("port")), name=base["name"], timeout=timeout)


def _build_http(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    kwargs: Dict[str, Any] = {}
    if "expect_status" in o:
        kwargs["expected_status"] = _status_list(o.pop("expect_status"))
    if "headers" in o:
        kwargs["headers"] = dict(o.pop("headers"))
    return HTTPCheck(
        url=o.pop("url"),
        name=base["name"],
        method=o.pop("method", "GET"),
        verify_ssl=bool(o.pop("verify_ssl", True)),
        contains_text=o.pop("match", None),
        timeout=timeout,
        **kwargs,
    )


def _build_tls(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    extra: Dict[str, Any] = {"warn_days": int(o.pop("warn_days", 30)), "crit_days": int(o.pop("crit_days", 7))}
    if "target" in o:
        return TLSCheck.from_target(o.pop("target"), name=base["name"], timeout=timeout, **extra)
    return TLSCheck(host=o.pop("host"), port=int(o.pop("port", 443)), name=base["name"], timeout=timeout, **extra)


def _build_ssh(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    return SSHCheck.from_target(
        o.pop("target"),
        name=base["name"],
        username=o.pop("user", None),
        key_path=o.pop("key", None),
        check_auth=bool(o.pop("auth", False)),
        timeout=timeout,
    )


def _build_exec(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    return ExecCheck(
        command=o.pop("command"),
        name=base["name"],
        expect_exit=int(o.pop("expect_exit", 0)),
        contains_text=o.pop("match", None),
        shell=bool(o.pop("shell", False)),
        cwd=o.pop("cwd", None),
        timeout=timeout,
    )


def _build_dns(o: Dict[str, Any], timeout: float, base: Dict[str, Any]) -> BaseCheck:
    return DNSCheck(host=o.pop("host"), name=base["name"], expect=o.pop("expect", None), timeout=timeout)


_BUILDERS: Dict[str, Callable[[Dict[str, Any], float, Dict[str, Any]], BaseCheck]] = {
    "system": _build_system,
    "ping": _build_ping,
    "tcp": _build_tcp,
    "http": _build_http,
    "tls": _build_tls,
    "ssh": _build_ssh,
    "exec": _build_exec,
    "dns": _build_dns,
}


def build_check(entry: Dict[str, Any], default_timeout: float, index: int = 0) -> BaseCheck:
    """Build one check from a config table; raises ConfigError on bad input."""
    where = f"check #{index + 1}"
    o = dict(entry)
    ctype = o.pop("type", None)
    if ctype not in _BUILDERS:
        raise ConfigError(f"{where}: unknown type {ctype!r} (expected one of {', '.join(_BUILDERS)})")
    label = f"{where} ({ctype})"

    base = {"name": o.pop("name", None)}
    stage = o.pop("stage", 0)
    enabled = o.pop("enabled", True)
    timeout = float(o.pop("timeout", default_timeout))
    if not isinstance(stage, int) or isinstance(stage, bool):
        raise ConfigError(f"{label}: 'stage' must be an integer")

    try:
        check = _BUILDERS[ctype](o, timeout, base)
    except KeyError as exc:
        raise ConfigError(f"{label}: missing required key {exc.args[0]!r}") from None
    except (TypeError, ValueError) as exc:
        if isinstance(exc, ConfigError):
            raise ConfigError(f"{label}: {exc}") from None
        raise ConfigError(f"{label}: invalid value ({exc})") from None
    if o:
        raise ConfigError(f"{label}: unknown key(s): {', '.join(sorted(o))}")
    check.stage = stage
    check.enabled = bool(enabled)
    return check


def load_config(path: str, timeout_override: Optional[float] = None) -> Tuple[Dict[str, Any], List[BaseCheck]]:
    """Parse a TOML file into (settings, checks)."""
    try:
        data = tomllib.loads(Path(path).read_text(encoding="utf-8"))
    except OSError as exc:
        raise ConfigError(f"cannot read {path}: {exc.strerror or exc}") from None
    except tomllib.TOMLDecodeError as exc:
        raise ConfigError(f"{path}: invalid TOML: {exc}") from None

    settings = dict(data.get("settings", {}))
    unknown = set(settings) - SETTINGS_KEYS
    if unknown:
        raise ConfigError(f"[settings]: unknown key(s): {', '.join(sorted(unknown))}")
    if settings.get("fail_on", "failure") not in ("failure", "warning"):
        raise ConfigError("[settings]: fail_on must be 'failure' or 'warning'")

    entries = data.get("check", [])
    if not isinstance(entries, list) or not entries:
        raise ConfigError(f"{path}: no [[check]] entries defined")
    timeout = float(timeout_override if timeout_override is not None else settings.get("timeout", 6.0))
    return settings, [build_check(e, timeout, i) for i, e in enumerate(entries)]
