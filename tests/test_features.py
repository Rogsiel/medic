"""Tests for timeouts, stages, exec/dns checks, config loading, junit, fail-on."""

import io
import sys
import time
from xml.etree import ElementTree as ET

import pytest

from medic.checks.dns import DNSCheck
from medic.checks.exec import ExecCheck
from medic.cli import main
from medic.config import ConfigError, load_config
from medic.core.base import BaseCheck
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult, MedicReport
from medic.core.runner import MedicRunner
from medic.formatters.junit_formatter import JUnitFormatter


class Hang(BaseCheck):
    name = "hang"
    category = CheckCategory.SYSTEM

    def run(self):
        time.sleep(30)


class Fixed(BaseCheck):
    category = CheckCategory.SYSTEM

    def __init__(self, name, status, stage=0):
        super().__init__(name=name)
        self.status = status
        self.stage = stage

    def run(self):
        return CheckResult(self.name, self.category, self.status, "x")


def test_runner_enforces_timeout(monkeypatch):
    monkeypatch.setattr("medic.core.runner.TIMEOUT_GRACE_SEC", 0.0)
    chk = Hang(timeout=0.2)
    start = time.perf_counter()
    report = MedicRunner(checks=[chk]).run()
    assert time.perf_counter() - start < 5
    assert report.results[0].status == CheckStatus.FAILED
    assert "Timed out" in report.results[0].message


def test_stage_failure_skips_later_stages():
    checks = [
        Fixed("a", CheckStatus.FAILED, stage=0),
        Fixed("b", CheckStatus.PASSED, stage=0),
        Fixed("c", CheckStatus.PASSED, stage=1),
    ]
    report = MedicRunner(checks=checks).run()
    by_name = {r.name: r.status for r in report.results}
    assert by_name == {"a": CheckStatus.FAILED, "b": CheckStatus.PASSED, "c": CheckStatus.SKIPPED}


def test_stop_on_failure_applies_in_parallel_mode():
    checks = [Fixed("a", CheckStatus.FAILED), Fixed("b", CheckStatus.PASSED)]
    report = MedicRunner(checks=checks, parallel=True, stop_on_failure=True).run()
    assert [r.name for r in report.results] == ["a"]


def test_exec_check_exit_codes():
    py = sys.executable
    assert ExecCheck([py, "-c", "print('ok')"], contains_text="ok").run().status == CheckStatus.PASSED
    res = ExecCheck([py, "-c", "import sys; sys.exit(3)"]).run()
    assert res.status == CheckStatus.FAILED and "3" in res.message
    assert ExecCheck([py, "-c", "import sys; sys.exit(3)"], expect_exit=3).run().status == CheckStatus.PASSED
    assert ExecCheck("definitely-not-a-binary-xyz").run().status == CheckStatus.FAILED


def test_dns_localhost():
    assert DNSCheck("localhost").run().status in (CheckStatus.PASSED, CheckStatus.WARNING)
    assert DNSCheck("localhost", expect=["203.0.113.9"]).run().status == CheckStatus.FAILED


def test_config_loads_stages_and_settings(tmp_path):
    f = tmp_path / "m.toml"
    f.write_text(
        '[settings]\nfail_on = "warning"\n'
        '[[check]]\ntype = "exec"\ncommand = "true"\n'
        '[[check]]\ntype = "tcp"\ntarget = "localhost:1"\nstage = 1\nname = "T"\n'
    )
    settings, checks = load_config(str(f))
    assert settings["fail_on"] == "warning"
    assert [c.stage for c in checks] == [0, 1]
    assert checks[1].name == "T"


@pytest.mark.parametrize(
    "body, msg",
    [
        ('[[check]]\ntype = "nope"\n', "unknown type"),
        ('[[check]]\ntype = "tcp"\n', "missing required key"),
        ('[[check]]\ntype = "exec"\ncommand = "true"\nbogus = 1\n', "unknown key"),
        ('[settings]\nfail_on = "x"\n[[check]]\ntype = "exec"\ncommand = "true"\n', "fail_on"),
        ("", "no [[check]]"),
    ],
)
def test_config_errors(tmp_path, body, msg):
    f = tmp_path / "m.toml"
    f.write_text(body)
    with pytest.raises(ConfigError, match=msg.replace("[", r"\[").replace("]", r"\]")):
        load_config(str(f))


def test_cli_config_end_to_end_and_fail_on(tmp_path, capsys):
    f = tmp_path / "m.toml"
    f.write_text('[[check]]\ntype = "exec"\ncommand = "true"\n')
    assert main(["--config", str(f), "--format", "json"]) == 0
    assert main(["--config", str(tmp_path / "missing.toml")]) == 2


def test_fail_on_warning_exit_code():
    report = MedicReport(results=[CheckResult("w", CheckCategory.SYSTEM, CheckStatus.WARNING, "m")])
    assert report.exit_code_for("failure") == 0
    assert report.exit_code_for("warning") == 1


def test_junit_output():
    report = MedicReport(
        results=[
            CheckResult("ok", CheckCategory.TCP, CheckStatus.PASSED, "m"),
            CheckResult("bad", CheckCategory.HTTP, CheckStatus.FAILED, "down"),
            CheckResult("skip", CheckCategory.HTTP, CheckStatus.SKIPPED, "s"),
        ]
    )
    buf = io.StringIO()
    JUnitFormatter(stream=buf).render(report)
    suite = ET.fromstring(buf.getvalue())
    assert suite.get("tests") == "3" and suite.get("failures") == "1" and suite.get("skipped") == "1"
    assert suite.find("./testcase[@name='bad']/failure").get("message") == "down"


def test_default_suite_is_local_only():
    from medic.cli import build_checks_from_args, build_parser

    checks = build_checks_from_args(build_parser().parse_args([]))
    assert {c.category for c in checks} == {CheckCategory.SYSTEM}
