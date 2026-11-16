"""Core enumeration types for medic."""

from enum import Enum


class CheckStatus(str, Enum):
    """Status of a health check result."""
    PASSED = "passed"
    WARNING = "warning"
    FAILED = "failed"
    SKIPPED = "skipped"
    ERROR = "error"

    @property
    def is_healthy(self) -> bool:
        """Returns True if the check indicates healthy state."""
        return self in (CheckStatus.PASSED, CheckStatus.WARNING)

    @property
    def icon(self) -> str:
        """Unicode icon representing the status."""
        return {
            CheckStatus.PASSED: "✔",
            CheckStatus.WARNING: "⚠",
            CheckStatus.FAILED: "✖",
            CheckStatus.SKIPPED: "⊘",
            CheckStatus.ERROR: "⚡",
        }[self]

    @property
    def color(self) -> str:
        """Rich style color corresponding to status."""
        return {
            CheckStatus.PASSED: "green",
            CheckStatus.WARNING: "yellow",
            CheckStatus.FAILED: "red",
            CheckStatus.SKIPPED: "dim",
            CheckStatus.ERROR: "bold red",
        }[self]


class CheckCategory(str, Enum):
    """Categories of health checks supported by medic."""
    SYSTEM = "system"
    PING = "ping"
    TCP = "tcp"
    HTTP = "http"
    TLS = "tls"
    SSH = "ssh"
    DOCKER = "docker"
    DNS = "dns"
    EXEC = "exec"

    @property
    def display_name(self) -> str:
        """Human-readable display title for category."""
        return {
            CheckCategory.SYSTEM: "System",
            CheckCategory.PING: "Ping",
            CheckCategory.TCP: "TCP",
            CheckCategory.HTTP: "HTTP",
            CheckCategory.TLS: "TLS/SSL",
            CheckCategory.SSH: "SSH",
            CheckCategory.DOCKER: "Docker",
            CheckCategory.DNS: "DNS",
            CheckCategory.EXEC: "Exec",
        }[self]


class OutputFormat(str, Enum):
    """Supported output formats."""
    RICH = "rich"
    JSON = "json"
    TEXT = "text"
    COMPACT = "compact"
    JUNIT = "junit"


class Severity(str, Enum):
    """Severity levels for checks and warnings."""
    INFO = "info"
    LOW = "low"
    MEDIUM = "medium"
    HIGH = "high"
    CRITICAL = "critical"
