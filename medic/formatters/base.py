"""Base interface for report formatters."""

from abc import ABC, abstractmethod

from medic.core.models import MedicReport


class BaseFormatter(ABC):
    """Abstract base class for output formatters."""

    @abstractmethod
    def render(self, report: MedicReport) -> None:
        """Render the report to stdout/terminal."""
        raise NotImplementedError
