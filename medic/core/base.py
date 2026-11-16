"""Abstract base classes and interfaces for medic checks."""

import time
from abc import ABC, abstractmethod
from contextlib import contextmanager
from typing import Generator, List, Optional, Union

from .enums import CheckCategory
from .models import CheckResult


@contextmanager
def timed_execution() -> Generator[dict, None, None]:
    """Context manager to measure execution time in milliseconds."""
    tracker = {"elapsed_ms": 0.0}
    start = time.perf_counter()
    try:
        yield tracker
    finally:
        end = time.perf_counter()
        tracker["elapsed_ms"] = (end - start) * 1000.0


class BaseCheck(ABC):
    """Abstract base class for all health checks."""

    name: str
    category: CheckCategory
    description: str = ""
    timeout: float = 10.0
    enabled: bool = True
    stage: int = 0

    def __init__(
        self,
        name: Optional[str] = None,
        timeout: float = 10.0,
        enabled: bool = True,
    ) -> None:
        if name is not None:
            self.name = name
        self.timeout = timeout
        self.enabled = enabled

    @abstractmethod
    def run(self) -> Union[CheckResult, List[CheckResult]]:
        """Execute the check and return one or more CheckResult objects."""
        raise NotImplementedError("Subclasses must implement run()")
