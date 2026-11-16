"""Tests for system health checks."""

from unittest.mock import MagicMock, patch

from medic.checks.system import (
    CPUCheck,
    DiskCheck,
    MemoryCheck,
    SystemCheckGroup,
    UptimeCheck,
    _format_bytes,
    _format_seconds,
)
from medic.core.enums import CheckCategory, CheckStatus


def test_format_bytes():
    assert _format_bytes(500) == "500.0 B"
    assert _format_bytes(1024 * 1024) == "1.0 MB"
    assert _format_bytes(1024 * 1024 * 1024 * 2.5) == "2.5 GB"


def test_format_seconds():
    assert _format_seconds(45) == "45s"
    assert _format_seconds(125) == "2m 5s"
    assert _format_seconds(3665) == "1h 1m 5s"
    assert _format_seconds(90065) == "1d 1h 1m 5s"


def test_cpu_check_real():
    chk = CPUCheck(warn_percent=99.0, crit_percent=100.0, interval=0.1)
    res = chk.run()
    assert res.category == CheckCategory.SYSTEM
    assert res.target == "cpu"
    assert "cpu_percent" in res.details
    assert res.status in (CheckStatus.PASSED, CheckStatus.WARNING, CheckStatus.FAILED)


@patch("medic.checks.system.psutil.cpu_percent", return_value=96.5)
@patch("medic.checks.system.os.getloadavg", return_value=(4.5, 3.2, 2.1))
def test_cpu_check_critical(mock_load, mock_cpu):
    chk = CPUCheck(warn_percent=80.0, crit_percent=95.0, interval=0.01)
    res = chk.run()
    assert res.status == CheckStatus.FAILED
    assert "CPU Usage: 96.5%" in res.message
    assert res.remediation is not None


def test_memory_check_real():
    chk = MemoryCheck(warn_percent=99.0, crit_percent=100.0)
    res = chk.run()
    assert res.category == CheckCategory.SYSTEM
    assert res.target == "memory"
    assert "used_percent" in res.details
    assert "total_bytes" in res.details


@patch("medic.checks.system.psutil.virtual_memory")
def test_memory_check_warning(mock_vm):
    mock_vm.return_value = MagicMock(
        percent=88.0,
        used=14 * 1024**3,
        total=16 * 1024**3,
        available=2 * 1024**3,
    )
    chk = MemoryCheck(warn_percent=85.0, crit_percent=95.0)
    res = chk.run()
    assert res.status == CheckStatus.WARNING
    assert res.remediation is not None


def test_disk_check_real():
    chk = DiskCheck(path="/", warn_percent=99.0, crit_percent=100.0)
    res = chk.run()
    assert res.category == CheckCategory.SYSTEM
    assert res.target == "/"
    assert res.status in (CheckStatus.PASSED, CheckStatus.WARNING, CheckStatus.FAILED)


def test_disk_check_nonexistent_path():
    chk = DiskCheck(path="/nonexistent_path_for_medic_test_12345")
    res = chk.run()
    assert res.status == CheckStatus.FAILED
    assert "does not exist" in res.message


def test_uptime_check_real():
    chk = UptimeCheck()
    res = chk.run()
    assert res.category == CheckCategory.SYSTEM
    assert res.target == "system"
    assert "uptime_seconds" in res.details
    assert res.details["uptime_seconds"] > 0


def test_system_check_group_real():
    group = SystemCheckGroup(disk_paths=["/"])
    results = group.run()
    assert len(results) >= 5
    categories = {r.category for r in results}
    assert CheckCategory.SYSTEM in categories
