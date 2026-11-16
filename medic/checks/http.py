"""HTTP and HTTPS endpoint health checks."""

import re
import time
import urllib.parse
from typing import Any, Dict, Optional, Sequence, Union

import requests
from requests.exceptions import ConnectionError, SSLError, Timeout

from medic.core.base import BaseCheck, timed_execution
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult


def normalize_url(url: str, default_scheme: str = "https") -> str:
    """Ensure URL has a valid scheme."""
    url = url.strip()
    if not re.match(r"^[a-zA-Z][a-zA-Z0-9\+\-\.]*://", url):
        url = f"{default_scheme}://{url}"
    return url


class HTTPCheck(BaseCheck):
    """Checks HTTP/HTTPS endpoint availability, response code, latency, and content."""

    category = CheckCategory.HTTP

    def __init__(
        self,
        url: str,
        name: Optional[str] = None,
        method: str = "GET",
        expected_status: Union[int, Sequence[int]] = (200, 201, 204, 301, 302, 307, 308),
        follow_redirects: bool = True,
        verify_ssl: bool = True,
        contains_text: Optional[str] = None,
        headers: Optional[Dict[str, str]] = None,
        timeout: float = 10.0,
        warn_latency_ms: float = 1000.0,
        crit_latency_ms: float = 3000.0,
        enabled: bool = True,
    ) -> None:
        self.raw_url = url
        self.url = normalize_url(url)
        parsed = urllib.parse.urlparse(self.url)
        display_target = f"{parsed.scheme}://{parsed.netloc}{parsed.path or '/'}"
        chk_name = name or f"HTTP {display_target}"
        super().__init__(name=chk_name, timeout=timeout, enabled=enabled)

        self.method = method.upper()
        if isinstance(expected_status, int):
            self.expected_status: Sequence[int] = [expected_status]
        else:
            self.expected_status = expected_status
        self.follow_redirects = follow_redirects
        self.verify_ssl = verify_ssl
        self.contains_text = contains_text
        self.headers = headers or {
            "User-Agent": "Medic-HealthCheck/0.1.0 (Python)",
            "Accept": "*/*",
        }
        self.warn_latency_ms = warn_latency_ms
        self.crit_latency_ms = crit_latency_ms

    def run(self) -> CheckResult:
        with timed_execution() as timer:
            try:
                session = requests.Session()
                req_start = time.perf_counter()
                resp = session.request(
                    method=self.method,
                    url=self.url,
                    headers=self.headers,
                    timeout=self.timeout,
                    allow_redirects=self.follow_redirects,
                    verify=self.verify_ssl,
                )
                req_ms = (time.perf_counter() - req_start) * 1000.0

            except SSLError as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"SSL certificate verification failed: {e}",
                    target=self.url,
                    latency_ms=timer["elapsed_ms"],
                    remediation="Verify SSL certificate expiration, SANs, or CA trust bundle (use --no-verify-ssl to bypass).",
                )
            except Timeout:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"HTTP request timed out after {self.timeout:.1f}s",
                    target=self.url,
                    latency_ms=timer["elapsed_ms"],
                    remediation="Endpoint took too long to respond. Check server load, slow database queries, or upstream reverse proxy.",
                )
            except ConnectionError as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Connection failed: {e}",
                    target=self.url,
                    latency_ms=timer["elapsed_ms"],
                    remediation=f"Could not establish connection to {self.url}. Ensure host is up and web server is running.",
                )
            except Exception as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.ERROR,
                    message=f"HTTP error: {e}",
                    target=self.url,
                    latency_ms=timer["elapsed_ms"],
                )

            status_code = resp.status_code
            status = CheckStatus.PASSED
            remediation = None

            # Verify status code
            if status_code not in self.expected_status:
                if 500 <= status_code < 600:
                    status = CheckStatus.FAILED
                    remediation = f"Server returned 5xx error ({status_code}). Check application logs and backend services."
                elif 400 <= status_code < 500:
                    status = CheckStatus.FAILED
                    remediation = f"Client error ({status_code}). Check route path, authentication headers, or permissions."
                else:
                    status = CheckStatus.FAILED
                    remediation = f"Unexpected HTTP status code {status_code} (expected: {list(self.expected_status)})."

            # Verify body content if requested
            body_text = resp.text
            if self.contains_text and status == CheckStatus.PASSED:
                if self.contains_text not in body_text:
                    status = CheckStatus.FAILED
                    remediation = f"Response body does not contain expected substring '{self.contains_text}'."

            # Verify latency
            if status == CheckStatus.PASSED:
                if req_ms >= self.crit_latency_ms:
                    status = CheckStatus.FAILED
                    remediation = f"HTTP response time ({req_ms:.1f}ms) is critically slow."
                elif req_ms >= self.warn_latency_ms:
                    status = CheckStatus.WARNING
                    remediation = f"HTTP response time ({req_ms:.1f}ms) exceeds warning threshold."

            redirect_info = ""
            if resp.history:
                redirect_count = len(resp.history)
                redirect_info = f" ({redirect_count} redirect{'s' if redirect_count > 1 else ''} -> {resp.url})"

            msg = f"HTTP {self.method} {status_code} in {req_ms:.1f}ms{redirect_info}"

            details: Dict[str, Any] = {
                "url": self.url,
                "final_url": str(resp.url),
                "status_code": status_code,
                "reason": resp.reason,
                "latency_ms": round(req_ms, 2),
                "headers": dict(resp.headers),
                "redirect_count": len(resp.history),
                "content_length": len(resp.content),
                "content_type": resp.headers.get("Content-Type"),
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target=self.url,
            latency_ms=req_ms,
            details=details,
            remediation=remediation,
        )
