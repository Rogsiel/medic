"""JSON output formatter for machine consumption and CI/CD pipelines."""

import json
import sys
from typing import Optional, TextIO

from medic.core.models import MedicReport

from .base import BaseFormatter


class JSONFormatter(BaseFormatter):
    """Formats report as formatted JSON."""

    def __init__(self, stream: Optional[TextIO] = None, indent: int = 2) -> None:
        self._stream = stream
        self.indent = indent

    @property
    def stream(self) -> TextIO:
        return self._stream if self._stream is not None else sys.stdout

    def render(self, report: MedicReport) -> None:
        payload = report.to_dict()
        self.stream.write(json.dumps(payload, indent=self.indent) + "\n")
        self.stream.flush()
