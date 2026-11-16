"""medic: Fast, type-safe CLI health check & diagnostic tool."""

from importlib.metadata import PackageNotFoundError, version

try:
    __version__ = version("medic-cli")
except PackageNotFoundError:  # running from a source tree without install metadata
    __version__ = "0+unknown"

from .checks.http import HTTPCheck
from .checks.ping import PingCheck
from .checks.ssh import SSHCheck
from .checks.system import CPUCheck, DiskCheck, MemoryCheck, SwapCheck, SystemCheckGroup, UptimeCheck
from .checks.tcp import TCPCheck
from .checks.tls import TLSCheck
from .core.base import BaseCheck
from .core.enums import CheckCategory, CheckStatus, OutputFormat
from .core.models import CheckResult, MedicReport
from .core.runner import MedicRunner

__all__ = [
    "__version__",
    "CheckCategory",
    "CheckStatus",
    "OutputFormat",
    "CheckResult",
    "MedicReport",
    "MedicRunner",
    "BaseCheck",
    "CPUCheck",
    "MemoryCheck",
    "DiskCheck",
    "SwapCheck",
    "UptimeCheck",
    "SystemCheckGroup",
    "PingCheck",
    "TCPCheck",
    "HTTPCheck",
    "TLSCheck",
    "SSHCheck",
]
