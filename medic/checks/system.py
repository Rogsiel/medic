"""System resource health checks (CPU, Memory, Disk, Swap, Uptime)."""

import os
import shutil
import time
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional, Tuple

try:
    import psutil
    HAS_PSUTIL = True
except ImportError:
    HAS_PSUTIL = False

from medic.core.base import BaseCheck, timed_execution
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult


def _format_bytes(bytes_val: float) -> str:
    """Format bytes into human-readable string (KB, MB, GB, TB)."""
    for unit in ["B", "KB", "MB", "GB", "TB", "PB"]:
        if bytes_val < 1024.0:
            return f"{bytes_val:.1f} {unit}"
        bytes_val /= 1024.0
    return f"{bytes_val:.1f} PB"


def _format_seconds(seconds: float) -> str:
    """Format seconds into readable days, hours, minutes."""
    days, rem = divmod(int(seconds), 86400)
    hours, rem = divmod(rem, 3600)
    minutes, secs = divmod(rem, 60)
    parts = []
    if days > 0:
        parts.append(f"{days}d")
    if hours > 0:
        parts.append(f"{hours}h")
    if minutes > 0:
        parts.append(f"{minutes}m")
    if secs > 0 or not parts:
        parts.append(f"{secs}s")
    return " ".join(parts)


class CPUCheck(BaseCheck):
    """Checks CPU utilization and system load average."""

    category = CheckCategory.SYSTEM

    def __init__(
        self,
        name: str = "System CPU",
        warn_percent: float = 80.0,
        crit_percent: float = 95.0,
        interval: float = 0.2,
        timeout: float = 5.0,
        enabled: bool = True,
    ) -> None:
        super().__init__(name=name, timeout=timeout, enabled=enabled)
        self.warn_percent = warn_percent
        self.crit_percent = crit_percent
        self.interval = interval

    def run(self) -> CheckResult:
        with timed_execution() as timer:
            if not HAS_PSUTIL:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.ERROR,
                    message="psutil library is required for CPU checks",
                    target="cpu",
                    latency_ms=timer["elapsed_ms"],
                )

            cpu_pct = psutil.cpu_percent(interval=self.interval)
            cpu_count = psutil.cpu_count(logical=True) or 1
            cpu_count_phys = psutil.cpu_count(logical=False) or cpu_count

            # Load averages
            load_avg: Tuple[float, float, float]
            try:
                load_avg = os.getloadavg()
            except (AttributeError, OSError):
                load_avg = (0.0, 0.0, 0.0)

            # Determine status
            status = CheckStatus.PASSED
            remediation = None

            if cpu_pct >= self.crit_percent:
                status = CheckStatus.FAILED
                remediation = "High CPU load. Identify heavy processes with: ps aux --sort=-%cpu | head -n 10"
            elif cpu_pct >= self.warn_percent:
                status = CheckStatus.WARNING
                remediation = "Elevated CPU usage. Monitor system activity with: top -b -n 1 | head -n 20"

            msg = f"CPU Usage: {cpu_pct:.1f}% (Load: {load_avg[0]:.2f}, {load_avg[1]:.2f}, {load_avg[2]:.2f})"

            details: Dict[str, Any] = {
                "cpu_percent": cpu_pct,
                "logical_cores": cpu_count,
                "physical_cores": cpu_count_phys,
                "load_1m": load_avg[0],
                "load_5m": load_avg[1],
                "load_15m": load_avg[2],
                "warn_threshold": self.warn_percent,
                "crit_threshold": self.crit_percent,
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target="cpu",
            latency_ms=timer["elapsed_ms"],
            details=details,
            remediation=remediation,
        )


class MemoryCheck(BaseCheck):
    """Checks RAM (virtual memory) utilization."""

    category = CheckCategory.SYSTEM

    def __init__(
        self,
        name: str = "System Memory",
        warn_percent: float = 85.0,
        crit_percent: float = 95.0,
        timeout: float = 5.0,
        enabled: bool = True,
    ) -> None:
        super().__init__(name=name, timeout=timeout, enabled=enabled)
        self.warn_percent = warn_percent
        self.crit_percent = crit_percent

    def run(self) -> CheckResult:
        with timed_execution() as timer:
            if not HAS_PSUTIL:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.ERROR,
                    message="psutil library is required for Memory checks",
                    target="memory",
                    latency_ms=timer["elapsed_ms"],
                )

            vm = psutil.virtual_memory()
            used_pct = vm.percent
            used_str = _format_bytes(vm.used)
            total_str = _format_bytes(vm.total)
            avail_str = _format_bytes(vm.available)

            status = CheckStatus.PASSED
            remediation = None

            if used_pct >= self.crit_percent:
                status = CheckStatus.FAILED
                remediation = "Critical RAM usage. Check memory leaks with: ps aux --sort=-%mem | head -n 10"
            elif used_pct >= self.warn_percent:
                status = CheckStatus.WARNING
                remediation = "Elevated RAM usage. Consider clearing caches or freeing unused buffers."

            msg = f"RAM: {used_pct:.1f}% used ({used_str} / {total_str}, {avail_str} available)"

            details: Dict[str, Any] = {
                "used_percent": used_pct,
                "used_bytes": vm.used,
                "total_bytes": vm.total,
                "available_bytes": vm.available,
                "used_human": used_str,
                "total_human": total_str,
                "available_human": avail_str,
                "warn_threshold": self.warn_percent,
                "crit_threshold": self.crit_percent,
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target="memory",
            latency_ms=timer["elapsed_ms"],
            details=details,
            remediation=remediation,
        )


class SwapCheck(BaseCheck):
    """Checks Swap space utilization."""

    category = CheckCategory.SYSTEM

    def __init__(
        self,
        name: str = "System Swap",
        warn_percent: float = 75.0,
        crit_percent: float = 90.0,
        timeout: float = 5.0,
        enabled: bool = True,
    ) -> None:
        super().__init__(name=name, timeout=timeout, enabled=enabled)
        self.warn_percent = warn_percent
        self.crit_percent = crit_percent

    def run(self) -> CheckResult:
        with timed_execution() as timer:
            if not HAS_PSUTIL:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.ERROR,
                    message="psutil library is required for Swap checks",
                    target="swap",
                    latency_ms=timer["elapsed_ms"],
                )

            sw = psutil.swap_memory()
            if sw.total == 0:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.PASSED,
                    message="Swap: disabled / 0 B configured",
                    target="swap",
                    latency_ms=timer["elapsed_ms"],
                    details={"total_bytes": 0, "used_bytes": 0, "used_percent": 0.0},
                )

            used_pct = sw.percent
            used_str = _format_bytes(sw.used)
            total_str = _format_bytes(sw.total)
            free_str = _format_bytes(sw.free)

            status = CheckStatus.PASSED
            remediation = None

            if used_pct >= self.crit_percent:
                status = CheckStatus.FAILED
                remediation = "Swap space almost exhausted. System may face OOM kills."
            elif used_pct >= self.warn_percent:
                status = CheckStatus.WARNING
                remediation = "High swap activity indicates memory pressure."

            msg = f"Swap: {used_pct:.1f}% used ({used_str} / {total_str}, {free_str} free)"

            details = {
                "used_percent": used_pct,
                "used_bytes": sw.used,
                "total_bytes": sw.total,
                "free_bytes": sw.free,
                "used_human": used_str,
                "total_human": total_str,
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target="swap",
            latency_ms=timer["elapsed_ms"],
            details=details,
            remediation=remediation,
        )


class DiskCheck(BaseCheck):
    """Checks disk space usage for a given path or mount point."""

    category = CheckCategory.SYSTEM

    def __init__(
        self,
        path: str = "/",
        name: Optional[str] = None,
        warn_percent: float = 85.0,
        crit_percent: float = 92.0,
        timeout: float = 5.0,
        enabled: bool = True,
    ) -> None:
        chk_name = name or f"Disk ({path})"
        super().__init__(name=chk_name, timeout=timeout, enabled=enabled)
        self.path = path
        self.warn_percent = warn_percent
        self.crit_percent = crit_percent

    def run(self) -> CheckResult:
        with timed_execution() as timer:
            if not os.path.exists(self.path):
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Disk path does not exist: {self.path}",
                    target=self.path,
                    latency_ms=timer["elapsed_ms"],
                )

            if HAS_PSUTIL:
                usage = psutil.disk_usage(self.path)
                total = usage.total
                used = usage.used
                free = usage.free
                pct = usage.percent
            else:
                usage_sh = shutil.disk_usage(self.path)
                total = usage_sh.total
                used = usage_sh.used
                free = usage_sh.free
                pct = (used / total * 100.0) if total > 0 else 0.0

            total_str = _format_bytes(total)
            used_str = _format_bytes(used)
            free_str = _format_bytes(free)

            status = CheckStatus.PASSED
            remediation = None

            if pct >= self.crit_percent:
                status = CheckStatus.FAILED
                remediation = f"Critically low disk space on {self.path}. Clean logs and temporary files: du -sh {self.path}/* 2>/dev/null | sort -hr | head -n 10"
            elif pct >= self.warn_percent:
                status = CheckStatus.WARNING
                remediation = f"Disk {self.path} is filling up ({pct:.1f}%). Check large files."

            msg = f"Disk ({self.path}): {pct:.1f}% used ({used_str} / {total_str}, {free_str} free)"

            details: Dict[str, Any] = {
                "path": self.path,
                "used_percent": pct,
                "used_bytes": used,
                "total_bytes": total,
                "free_bytes": free,
                "used_human": used_str,
                "total_human": total_str,
                "free_human": free_str,
                "warn_threshold": self.warn_percent,
                "crit_threshold": self.crit_percent,
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target=self.path,
            latency_ms=timer["elapsed_ms"],
            details=details,
            remediation=remediation,
        )


class UptimeCheck(BaseCheck):
    """Checks system uptime and boot timestamp."""

    category = CheckCategory.SYSTEM

    def __init__(
        self,
        name: str = "System Uptime",
        min_uptime_seconds: float = 60.0,
        timeout: float = 5.0,
        enabled: bool = True,
    ) -> None:
        super().__init__(name=name, timeout=timeout, enabled=enabled)
        self.min_uptime_seconds = min_uptime_seconds

    def run(self) -> CheckResult:
        with timed_execution() as timer:
            if not HAS_PSUTIL:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.ERROR,
                    message="psutil library is required for Uptime check",
                    target="system",
                    latency_ms=timer["elapsed_ms"],
                )

            boot_time = psutil.boot_time()
            now = time.time()
            uptime_secs = max(0.0, now - boot_time)
            uptime_str = _format_seconds(uptime_secs)
            boot_dt = datetime.fromtimestamp(boot_time, tz=timezone.utc).strftime("%Y-%m-%d %H:%M:%S UTC")

            proc_count = len(psutil.pids())

            status = CheckStatus.PASSED
            remediation = None

            if uptime_secs < self.min_uptime_seconds:
                status = CheckStatus.WARNING
                remediation = f"System was rebooted recently ({uptime_str} ago). Check dmesg or system logs."

            msg = f"Uptime: {uptime_str} (Booted: {boot_dt}, Processes: {proc_count})"

            details: Dict[str, Any] = {
                "uptime_seconds": uptime_secs,
                "uptime_human": uptime_str,
                "boot_time": boot_dt,
                "process_count": proc_count,
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target="system",
            latency_ms=timer["elapsed_ms"],
            details=details,
            remediation=remediation,
        )


class SystemCheckGroup(BaseCheck):
    """Composite check that gathers all standard system health metrics."""

    category = CheckCategory.SYSTEM

    def __init__(
        self,
        name: str = "System Health",
        disk_paths: Optional[List[str]] = None,
        cpu_warn: float = 80.0,
        cpu_crit: float = 95.0,
        ram_warn: float = 85.0,
        ram_crit: float = 95.0,
        disk_warn: float = 85.0,
        disk_crit: float = 92.0,
        timeout: float = 10.0,
        enabled: bool = True,
    ) -> None:
        super().__init__(name=name, timeout=timeout, enabled=enabled)
        self.disk_paths = disk_paths or ["/"]
        self.cpu_warn = cpu_warn
        self.cpu_crit = cpu_crit
        self.ram_warn = ram_warn
        self.ram_crit = ram_crit
        self.disk_warn = disk_warn
        self.disk_crit = disk_crit

    def run(self) -> List[CheckResult]:
        sub_checks: List[BaseCheck] = [
            CPUCheck(warn_percent=self.cpu_warn, crit_percent=self.cpu_crit),
            MemoryCheck(warn_percent=self.ram_warn, crit_percent=self.ram_crit),
            SwapCheck(),
        ]
        for path in self.disk_paths:
            sub_checks.append(
                DiskCheck(path=path, warn_percent=self.disk_warn, crit_percent=self.disk_crit)
            )
        sub_checks.append(UptimeCheck())

        results: List[CheckResult] = []
        for chk in sub_checks:
            res = chk.run()
            if isinstance(res, list):
                results.extend(res)
            else:
                results.append(res)
        return results
