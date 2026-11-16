"""DNS resolution check."""

import socket
import time
from typing import List, Optional, Sequence

from medic.core.base import BaseCheck
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult


class DNSCheck(BaseCheck):
    """Resolves a hostname via the system resolver, optionally requiring specific addresses."""

    category = CheckCategory.DNS

    def __init__(
        self,
        host: str,
        name: Optional[str] = None,
        expect: Optional[Sequence[str]] = None,
        warn_latency_ms: float = 500.0,
        timeout: float = 5.0,
        enabled: bool = True,
    ) -> None:
        super().__init__(name=name or f"DNS {host}", timeout=timeout, enabled=enabled)
        self.host = host.strip()
        self.expect = list(expect) if expect else []
        self.warn_latency_ms = warn_latency_ms

    def run(self) -> CheckResult:
        start = time.perf_counter()
        try:
            infos = socket.getaddrinfo(self.host, None, proto=socket.IPPROTO_TCP)
        except socket.gaierror as exc:
            return self._result(CheckStatus.FAILED, f"Resolution failed: {exc.strerror or exc}", start, [])

        addrs: List[str] = sorted({str(i[4][0]) for i in infos})
        missing = [a for a in self.expect if a not in addrs]
        if missing:
            return self._result(
                CheckStatus.FAILED, f"Missing expected address(es): {', '.join(missing)}", start, addrs
            )
        elapsed = (time.perf_counter() - start) * 1000.0
        status = CheckStatus.WARNING if elapsed > self.warn_latency_ms else CheckStatus.PASSED
        return self._result(status, f"Resolved to {', '.join(addrs)}", start, addrs)

    def _result(self, status: CheckStatus, message: str, start: float, addrs: List[str]) -> CheckResult:
        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=message,
            target=self.host,
            latency_ms=(time.perf_counter() - start) * 1000.0,
            details={"addresses": addrs},
            remediation="Check resolver configuration (/etc/resolv.conf) and the DNS record."
            if status == CheckStatus.FAILED
            else None,
        )
