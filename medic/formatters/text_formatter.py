"""Plain text and compact console formatters."""

import sys
from typing import Optional, TextIO

from medic.core.enums import CheckStatus
from medic.core.models import MedicReport

from .base import BaseFormatter


class TextFormatter(BaseFormatter):
    """Simple plain-text table formatter without rich ANSI dependency."""

    def __init__(self, stream: Optional[TextIO] = None, compact: bool = False) -> None:
        self._stream = stream
        self.compact = compact

    @property
    def stream(self) -> TextIO:
        return self._stream if self._stream is not None else sys.stdout
    def render(self, report: MedicReport) -> None:
        out = self.stream

        if self.compact:
            for r in report.results:
                status_char = "+" if r.status == CheckStatus.PASSED else ("!" if r.status == CheckStatus.WARNING else "-")
                lat = f"{r.latency_ms:.1f}ms" if r.latency_ms is not None else "-"
                out.write(f"[{status_char}] {r.category.value.upper():<6} {r.name:<25} {lat:>8} : {r.message}\n")
            out.write(f"SUMMARY: {report.passed_count} passed, {report.warning_count} warn, {report.failed_count} fail, {report.error_count} err ({report.duration_ms:.1f}ms)\n")
            out.flush()
            return

        out.write("================================================================================\n")
        out.write(f" MEDIC Health Report - {report.timestamp.isoformat()}\n")
        out.write("================================================================================\n\n")

        # Table header
        out.write(f"{'STATUS':<8} {'CATEGORY':<10} {'CHECK NAME':<24} {'LATENCY':<10} {'MESSAGE'}\n")
        out.write("-" * 80 + "\n")

        for r in report.results:
            lat_str = f"{r.latency_ms:.1f}ms" if r.latency_ms is not None else "-"
            out.write(f"{r.status.value.upper():<8} {r.category.display_name:<10} {r.name[:23]:<24} {lat_str:<10} {r.message}\n")

        out.write("-" * 80 + "\n")

        # Remediation
        issues = [r for r in report.results if r.status in (CheckStatus.WARNING, CheckStatus.FAILED, CheckStatus.ERROR)]
        if issues:
            out.write("\nAction Items & Remediation:\n")
            for issue in issues:
                out.write(f"  * [{issue.status.value.upper()}] {issue.name}: {issue.message}\n")
                if issue.remediation:
                    out.write(f"    Fix: {issue.remediation}\n")
            out.write("\n")

        status_word = "HEALTHY" if report.is_healthy else "ISSUES DETECTED"
        out.write(f"\nResult: {status_word} ({report.passed_count} passed, {report.warning_count} warnings, {report.failed_count} failures in {report.duration_ms:.1f}ms)\n")
        out.flush()
