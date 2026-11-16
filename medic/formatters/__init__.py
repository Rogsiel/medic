"""Output formatters for medic reports."""

from typing import Optional

from rich.console import Console

from medic.core.enums import OutputFormat

from .base import BaseFormatter
from .json_formatter import JSONFormatter
from .junit_formatter import JUnitFormatter
from .rich_formatter import RichFormatter
from .text_formatter import TextFormatter


def get_formatter(
    fmt: OutputFormat,
    console: Optional[Console] = None,
    verbose: bool = False,
    show_remediation: bool = True,
    no_color: bool = False,
) -> BaseFormatter:
    """Factory function to instantiate appropriate formatter."""
    if fmt == OutputFormat.JSON:
        return JSONFormatter()
    elif fmt == OutputFormat.JUNIT:
        return JUnitFormatter()
    elif fmt == OutputFormat.TEXT:
        return TextFormatter(compact=False)
    elif fmt == OutputFormat.COMPACT:
        return TextFormatter(compact=True)
    else:
        return RichFormatter(
            console=console,
            verbose=verbose,
            show_remediation=show_remediation,
            no_color=no_color,
        )


__all__ = [
    "BaseFormatter",
    "RichFormatter",
    "JSONFormatter",
    "JUnitFormatter",
    "TextFormatter",
    "get_formatter",
]
