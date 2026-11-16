"""Core data models for check results and reports."""

from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

from .enums import CheckCategory, CheckStatus


@dataclass
class CheckResult:
    """Represents the outcome of an individual check."""
    name: str
    category: CheckCategory
    status: CheckStatus
    message: str
    target: str = ""
    latency_ms: Optional[float] = None
    details: Dict[str, Any] = field(default_factory=dict)
    timestamp: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    remediation: Optional[str] = None

    @property
    def is_success(self) -> bool:
        """Returns True if status is PASSED."""
        return self.status == CheckStatus.PASSED

    @property
    def is_warning(self) -> bool:
        """Returns True if status is WARNING."""
        return self.status == CheckStatus.WARNING

    @property
    def is_failure(self) -> bool:
        """Returns True if status is FAILED or ERROR."""
        return self.status in (CheckStatus.FAILED, CheckStatus.ERROR)

    def to_dict(self) -> Dict[str, Any]:
        """Convert result to JSON-serializable dictionary."""
        return {
            "name": self.name,
            "category": self.category.value,
            "status": self.status.value,
            "message": self.message,
            "target": self.target,
            "latency_ms": round(self.latency_ms, 2) if self.latency_ms is not None else None,
            "details": self.details,
            "timestamp": self.timestamp.isoformat(),
            "remediation": self.remediation,
        }


@dataclass
class MedicReport:
    """Aggregated health check report."""
    results: List[CheckResult] = field(default_factory=list)
    timestamp: datetime = field(default_factory=lambda: datetime.now(timezone.utc))
    duration_ms: float = 0.0

    @property
    def total_count(self) -> int:
        return len(self.results)

    @property
    def passed_count(self) -> int:
        return sum(1 for r in self.results if r.status == CheckStatus.PASSED)

    @property
    def warning_count(self) -> int:
        return sum(1 for r in self.results if r.status == CheckStatus.WARNING)

    @property
    def failed_count(self) -> int:
        return sum(1 for r in self.results if r.status == CheckStatus.FAILED)

    @property
    def skipped_count(self) -> int:
        return sum(1 for r in self.results if r.status == CheckStatus.SKIPPED)

    @property
    def error_count(self) -> int:
        return sum(1 for r in self.results if r.status == CheckStatus.ERROR)

    @property
    def is_healthy(self) -> bool:
        """Returns True if no checks failed or errored."""
        return self.failed_count == 0 and self.error_count == 0

    @property
    def exit_code(self) -> int:
        """Standard CLI exit code: 0 if healthy (or only warnings), 1 if any failure/error."""
        return 0 if self.is_healthy else 1

    def exit_code_for(self, fail_on: str = "failure") -> int:
        """Exit code under a failure policy: 'failure' (default) or 'warning' (warnings also fail)."""
        if fail_on == "warning" and self.warning_count:
            return 1
        return self.exit_code

    def to_dict(self) -> Dict[str, Any]:
        """Convert report to JSON-serializable dictionary."""
        return {
            "timestamp": self.timestamp.isoformat(),
            "duration_ms": round(self.duration_ms, 2),
            "summary": {
                "total": self.total_count,
                "passed": self.passed_count,
                "warning": self.warning_count,
                "failed": self.failed_count,
                "skipped": self.skipped_count,
                "error": self.error_count,
                "is_healthy": self.is_healthy,
                "exit_code": self.exit_code,
            },
            "results": [r.to_dict() for r in self.results],
        }
