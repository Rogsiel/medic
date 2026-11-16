"""Tests for MedicRunner."""

from typing import List

from medic.core.base import BaseCheck
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult
from medic.core.runner import MedicRunner


class DummyPassingCheck(BaseCheck):
    name = "Dummy Pass"
    category = CheckCategory.SYSTEM

    def run(self) -> CheckResult:
        return CheckResult(self.name, self.category, CheckStatus.PASSED, "All good")


class DummyFailingCheck(BaseCheck):
    name = "Dummy Fail"
    category = CheckCategory.TCP

    def run(self) -> CheckResult:
        return CheckResult(self.name, self.category, CheckStatus.FAILED, "Port closed")


class DummyMultiCheck(BaseCheck):
    name = "Dummy Multi"
    category = CheckCategory.SYSTEM

    def run(self) -> List[CheckResult]:
        return [
            CheckResult("Sub1", self.category, CheckStatus.PASSED, "sub1 ok"),
            CheckResult("Sub2", self.category, CheckStatus.PASSED, "sub2 ok"),
        ]


class DummyCrashingCheck(BaseCheck):
    name = "Dummy Crash"
    category = CheckCategory.HTTP

    def run(self) -> CheckResult:
        raise RuntimeError("Unexpected boom!")


def test_runner_sequential_execution():
    runner = MedicRunner(parallel=False)
    runner.add_check(DummyPassingCheck())
    runner.add_check(DummyMultiCheck())
    runner.add_check(DummyFailingCheck())

    report = runner.run()
    assert report.total_count == 4
    assert report.passed_count == 3
    assert report.failed_count == 1
    assert report.is_healthy is False


def test_runner_parallel_execution():
    runner = MedicRunner(parallel=True, max_workers=4)
    runner.add_check(DummyPassingCheck())
    runner.add_check(DummyMultiCheck())
    runner.add_check(DummyFailingCheck())

    report = runner.run()
    assert report.total_count == 4
    assert report.passed_count == 3
    assert report.failed_count == 1


def test_runner_exception_handling():
    runner = MedicRunner()
    runner.add_check(DummyCrashingCheck())

    report = runner.run()
    assert report.total_count == 1
    assert report.error_count == 1
    res = report.results[0]
    assert res.status == CheckStatus.ERROR
    assert "Unexpected boom!" in res.message
    assert res.details.get("exception") == "RuntimeError"


def test_runner_disabled_check():
    chk = DummyPassingCheck(enabled=False)
    runner = MedicRunner([chk])
    report = runner.run()
    assert report.total_count == 1
    assert report.skipped_count == 1


def test_runner_callback():
    observed = []
    runner = MedicRunner(
        [DummyPassingCheck(), DummyFailingCheck()],
        parallel=False,
        on_result=lambda r: observed.append(r.name),
    )
    runner.run()
    assert len(observed) == 2
    assert "Dummy Pass" in observed
    assert "Dummy Fail" in observed
