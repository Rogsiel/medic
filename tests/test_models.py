"""Tests for core data models and enums."""


from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult, MedicReport


def test_check_status_properties():
    assert CheckStatus.PASSED.is_healthy is True
    assert CheckStatus.WARNING.is_healthy is True
    assert CheckStatus.FAILED.is_healthy is False
    assert CheckStatus.ERROR.is_healthy is False
    assert CheckStatus.SKIPPED.is_healthy is False

    assert CheckStatus.PASSED.icon == "✔"
    assert CheckStatus.WARNING.icon == "⚠"
    assert CheckStatus.FAILED.icon == "✖"
    assert CheckStatus.ERROR.icon == "⚡"
    assert CheckStatus.SKIPPED.icon == "⊘"

    assert CheckStatus.PASSED.color == "green"
    assert CheckStatus.FAILED.color == "red"


def test_check_category_display():
    assert CheckCategory.SYSTEM.display_name == "System"
    assert CheckCategory.PING.display_name == "Ping"
    assert CheckCategory.TCP.display_name == "TCP"
    assert CheckCategory.HTTP.display_name == "HTTP"
    assert CheckCategory.TLS.display_name == "TLS/SSL"
    assert CheckCategory.SSH.display_name == "SSH"
    assert CheckCategory.DOCKER.display_name == "Docker"


def test_check_result_to_dict():
    res = CheckResult(
        name="Test CPU",
        category=CheckCategory.SYSTEM,
        status=CheckStatus.PASSED,
        message="CPU is normal",
        target="cpu",
        latency_ms=12.3456,
        details={"cpu_pct": 15.0},
        remediation=None,
    )
    assert res.is_success is True
    assert res.is_warning is False
    assert res.is_failure is False

    d = res.to_dict()
    assert d["name"] == "Test CPU"
    assert d["category"] == "system"
    assert d["status"] == "passed"
    assert d["message"] == "CPU is normal"
    assert d["target"] == "cpu"
    assert d["latency_ms"] == 12.35
    assert d["details"]["cpu_pct"] == 15.0
    assert "timestamp" in d


def test_medic_report_metrics():
    r1 = CheckResult("c1", CheckCategory.SYSTEM, CheckStatus.PASSED, "ok")
    r2 = CheckResult("c2", CheckCategory.HTTP, CheckStatus.WARNING, "slow")
    r3 = CheckResult("c3", CheckCategory.TCP, CheckStatus.FAILED, "refused")
    r4 = CheckResult("c4", CheckCategory.SSH, CheckStatus.SKIPPED, "skip")
    r5 = CheckResult("c5", CheckCategory.PING, CheckStatus.ERROR, "err")

    report = MedicReport(results=[r1, r2, r3, r4, r5], duration_ms=150.0)

    assert report.total_count == 5
    assert report.passed_count == 1
    assert report.warning_count == 1
    assert report.failed_count == 1
    assert report.skipped_count == 1
    assert report.error_count == 1
    assert report.is_healthy is False
    assert report.exit_code == 1

    d = report.to_dict()
    assert d["summary"]["total"] == 5
    assert d["summary"]["passed"] == 1
    assert d["summary"]["failed"] == 1
    assert d["summary"]["is_healthy"] is False
    assert d["duration_ms"] == 150.0
    assert len(d["results"]) == 5


def test_medic_report_healthy():
    r1 = CheckResult("c1", CheckCategory.SYSTEM, CheckStatus.PASSED, "ok")
    r2 = CheckResult("c2", CheckCategory.HTTP, CheckStatus.WARNING, "warn only")
    report = MedicReport(results=[r1, r2])
    assert report.is_healthy is True
    assert report.exit_code == 0
