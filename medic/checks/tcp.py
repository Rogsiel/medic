"""TCP socket connectivity, latency, and banner check."""

import socket
import time
from typing import Any, Dict, Optional, Tuple

from medic.core.base import BaseCheck, timed_execution
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult


def parse_host_port(target: str, default_port: int = 80) -> Tuple[str, int]:
    """Parse host and port from strings like 'example.com:443', '[::1]:80', or 'localhost'."""
    target = target.strip()
    if target.startswith("["):
        # IPv6 with brackets, e.g. [::1]:8080 or [::1]
        end_bracket = target.find("]")
        if end_bracket != -1:
            host = target[1:end_bracket]
            rem = target[end_bracket + 1 :]
            if rem.startswith(":"):
                port = int(rem[1:])
                return host, port
            return host, default_port

    if ":" in target:
        parts = target.rsplit(":", 1)
        try:
            return parts[0], int(parts[1])
        except ValueError:
            return parts[0], default_port

    return target, default_port


class TCPCheck(BaseCheck):
    """Checks TCP socket connectivity to a specified host:port."""

    category = CheckCategory.TCP

    def __init__(
        self,
        host: str,
        port: int = 80,
        name: Optional[str] = None,
        timeout: float = 5.0,
        warn_latency_ms: float = 200.0,
        crit_latency_ms: float = 1000.0,
        grab_banner: bool = False,
        banner_send_payload: Optional[bytes] = None,
        banner_max_bytes: int = 256,
        enabled: bool = True,
    ) -> None:
        chk_name = name or f"TCP {host}:{port}"
        super().__init__(name=chk_name, timeout=timeout, enabled=enabled)
        self.host = host.strip()
        self.port = port
        self.warn_latency_ms = warn_latency_ms
        self.crit_latency_ms = crit_latency_ms
        self.grab_banner = grab_banner
        self.banner_send_payload = banner_send_payload
        self.banner_max_bytes = banner_max_bytes

    @classmethod
    def from_target(cls, target: str, default_port: int = 80, **kwargs: Any) -> "TCPCheck":
        """Factory method to instantiate from 'host:port' string."""
        host, port = parse_host_port(target, default_port=default_port)
        return cls(host=host, port=port, **kwargs)

    def run(self) -> CheckResult:
        target_str = f"{self.host}:{self.port}"
        with timed_execution() as timer:
            banner_text: Optional[str] = None
            resolved_ip: Optional[str] = None

            try:
                # Resolve host first
                addr_info = socket.getaddrinfo(
                    self.host, self.port, socket.AF_UNSPEC, socket.SOCK_STREAM
                )
                if addr_info:
                    resolved_ip = str(addr_info[0][4][0])
            except socket.gaierror as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"DNS resolution failed: {e}",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                    remediation=f"Check DNS configuration or /etc/hosts for '{self.host}'.",
                )

            # Establish TCP connection
            sock: Optional[socket.socket] = None
            try:
                conn_start = time.perf_counter()
                sock = socket.create_connection((self.host, self.port), timeout=self.timeout)
                conn_ms = (time.perf_counter() - conn_start) * 1000.0

                # Optional banner grab
                if self.grab_banner:
                    sock.settimeout(min(self.timeout, 2.0))
                    if self.banner_send_payload:
                        sock.sendall(self.banner_send_payload)
                    try:
                        raw_banner = sock.recv(self.banner_max_bytes)
                        banner_text = raw_banner.decode("utf-8", errors="replace").strip()
                    except (TimeoutError, OSError):
                        banner_text = None

            except ConnectionRefusedError:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Connection refused on {target_str}",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                    details={"resolved_ip": resolved_ip, "port": self.port},
                    remediation=f"Verify that the service is running and listening on {target_str} (e.g. netstat -tulpn or ss -tulpn).",
                )
            except TimeoutError:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Connection timed out after {self.timeout:.1f}s to {target_str}",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                    details={"resolved_ip": resolved_ip, "port": self.port},
                    remediation=f"Check firewall rules (iptables, ufw, security groups) permitting ingress on port {self.port}.",
                )
            except OSError as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Socket error ({e.strerror or str(e)}) on {target_str}",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                    details={"resolved_ip": resolved_ip, "errno": getattr(e, "errno", None)},
                    remediation="Check routing table and network interface status.",
                )
            finally:
                if sock is not None:
                    try:
                        sock.close()
                    except Exception:
                        pass

            # Evaluate latency
            status = CheckStatus.PASSED
            remediation = None

            if conn_ms >= self.crit_latency_ms:
                status = CheckStatus.FAILED
                remediation = f"TCP handshake latency ({conn_ms:.1f}ms) is critically slow."
            elif conn_ms >= self.warn_latency_ms:
                status = CheckStatus.WARNING
                remediation = f"TCP handshake latency ({conn_ms:.1f}ms) is high."

            banner_info = f" [Banner: {banner_text[:30]}...]" if banner_text else ""
            ip_info = f" ({resolved_ip})" if resolved_ip and resolved_ip != self.host else ""
            msg = f"Connected to {target_str}{ip_info} in {conn_ms:.2f}ms{banner_info}"

            details: Dict[str, Any] = {
                "host": self.host,
                "port": self.port,
                "resolved_ip": resolved_ip,
                "handshake_ms": round(conn_ms, 2),
                "banner": banner_text,
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target=target_str,
            latency_ms=conn_ms,
            details=details,
            remediation=remediation,
        )
