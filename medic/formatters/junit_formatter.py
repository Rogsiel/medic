"""JUnit XML output formatter for CI systems."""

import sys
from typing import Optional, TextIO
from xml.etree import ElementTree as ET

from medic.core.enums import CheckStatus
from medic.core.models import MedicReport

from .base import BaseFormatter


class JUnitFormatter(BaseFormatter):
    """Formats a report as a JUnit <testsuite>. Warnings are passes with system-out."""

    def __init__(self, stream: Optional[TextIO] = None) -> None:
        self._stream = stream

    @property
    def stream(self) -> TextIO:
        return self._stream if self._stream is not None else sys.stdout

    def render(self, report: MedicReport) -> None:
        suite = ET.Element(
            "testsuite",
            name="medic",
            tests=str(report.total_count),
            failures=str(report.failed_count),
            errors=str(report.error_count),
            skipped=str(report.skipped_count),
            time=f"{report.duration_ms / 1000.0:.3f}",
            timestamp=report.timestamp.isoformat(),
        )
        for r in report.results:
            case = ET.SubElement(
                suite,
                "testcase",
                classname=r.category.value,
                name=r.name,
                time=f"{(r.latency_ms or 0.0) / 1000.0:.3f}",
            )
            if r.status == CheckStatus.FAILED:
                ET.SubElement(case, "failure", message=r.message).text = r.remediation or r.message
            elif r.status == CheckStatus.ERROR:
                ET.SubElement(case, "error", message=r.message).text = str(r.details.get("traceback", r.message))
            elif r.status == CheckStatus.SKIPPED:
                ET.SubElement(case, "skipped", message=r.message)
            elif r.status == CheckStatus.WARNING:
                ET.SubElement(case, "system-out").text = f"WARNING: {r.message}"
        ET.indent(suite)
        self.stream.write(ET.tostring(suite, encoding="unicode") + "\n")
        self.stream.flush()
