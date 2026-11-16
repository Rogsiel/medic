"""SSH service availability, banner, protocol version, and connectivity health checks."""

import re
import socket
import subprocess
import time
from typing import Any, Dict, Optional, Tuple

from medic.checks.tcp import parse_host_port
from medic.core.base import BaseCheck
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult


class SSHCheck(BaseCheck):
    """Checks SSH daemon availability, banner exchange, protocol version, and optional auth."""

    category = CheckCategory.SSH

    def __init__(
        self,
        host: str,
        port: int = 22,
        name: Optional[str] = None,
        username: Optional[str] = None,
        key_path: Optional[str] = None,
        check_auth: bool = False,
        timeout: float = 6.0,
        warn_latency_ms: float = 300.0,
        crit_latency_ms: float = 1500.0,
        enabled: bool = True,
    ) -> None:
        chk_name = name or f"SSH {host}:{port}"
        super().__init__(name=chk_name, timeout=timeout, enabled=enabled)
        self.host = host.strip()
        self.port = port
        self.username = username
        self.key_path = key_path
        self.check_auth = check_auth
        self.warn_latency_ms = warn_latency_ms
        self.crit_latency_ms = crit_latency_ms

    @classmethod
    def from_target(cls, target: str, default_port: int = 22, **kwargs: Any) -> "SSHCheck":
        """Factory method to instantiate from 'host:port' string."""
        host, port = parse_host_port(target, default_port=default_port)
        return cls(host=host, port=port, **kwargs)

    def _check_socket_banner(self) -> Tuple[CheckStatus, str, Optional[float], Dict[str, Any], Optional[str]]:
        """Connect to SSH port and read RFC 4253 protocol identification string."""
        target_str = f"{self.host}:{self.port}"
        start_time = time.perf_counter()
        sock: Optional[socket.socket] = None

        try:
            sock = socket.create_connection((self.host, self.port), timeout=self.timeout)
            sock.settimeout(min(self.timeout, 3.0))

            # Read server identification banner (max 255 bytes per RFC 4253)
            banner_bytes = bytearray()
            while len(banner_bytes) < 255:
                chunk = sock.recv(1)
                if not chunk:
                    break
                banner_bytes.extend(chunk)
                if banner_bytes.endswith(b"\n"):
                    break

            elapsed_ms = (time.perf_counter() - start_time) * 1000.0
            banner_str = banner_bytes.decode("utf-8", errors="replace").strip()

        except ConnectionRefusedError:
            elapsed_ms = (time.perf_counter() - start_time) * 1000.0
            return (
                CheckStatus.FAILED,
                f"SSH connection refused on {target_str}",
                elapsed_ms,
                {},
                "Check if sshd service is running (systemctl status sshd / service ssh status).",
            )
        except TimeoutError:
            elapsed_ms = (time.perf_counter() - start_time) * 1000.0
            return (
                CheckStatus.FAILED,
                f"SSH connection timed out after {self.timeout:.1f}s",
                elapsed_ms,
                {},
                f"Check firewall / security group allowing inbound port {self.port}.",
            )
        except Exception as e:
            elapsed_ms = (time.perf_counter() - start_time) * 1000.0
            return (
                CheckStatus.FAILED,
                f"SSH connection error: {e}",
                elapsed_ms,
                {},
                f"Could not connect to {target_str}.",
            )
        finally:
            if sock is not None:
                try:
                    # Send client protocol identification before close
                    sock.sendall(b"SSH-2.0-MedicHealthCheck_0.1.0\r\n")
                except Exception:
                    pass
                try:
                    sock.close()
                except Exception:
                    pass

        if not banner_str:
            return (
                CheckStatus.FAILED,
                f"Port {self.port} open but returned empty SSH banner",
                elapsed_ms,
                {},
                "Ensure an SSH daemon is listening on this port.",
            )

        # Parse banner RFC format: SSH-protoversion-softwareversion [comments]
        # Example: SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.4
        match = re.match(r"^SSH-([0-9\.]+)-(\S+)(?:\s+(.*))?$", banner_str)
        if not match:
            return (
                CheckStatus.FAILED,
                f"Invalid SSH banner received: '{banner_str[:50]}'",
                elapsed_ms,
                {"raw_banner": banner_str},
                f"Port {self.port} is open but does not appear to be an SSH service.",
            )

        proto_version = match.group(1)
        software_version = match.group(2)
        comment = match.group(3) or ""

        status = CheckStatus.PASSED
        remediation = None

        if proto_version.startswith("1."):
            status = CheckStatus.FAILED
            remediation = "Insecure legacy SSH protocol version 1.x detected. Upgrade to SSHv2."
        elif elapsed_ms >= self.crit_latency_ms:
            status = CheckStatus.FAILED
            remediation = f"SSH handshake response ({elapsed_ms:.1f}ms) is critically slow."
        elif elapsed_ms >= self.warn_latency_ms:
            status = CheckStatus.WARNING
            remediation = f"SSH handshake response ({elapsed_ms:.1f}ms) is slower than expected."

        msg = f"SSH-2.0 ({software_version}) reachable in {elapsed_ms:.1f}ms"
        details: Dict[str, Any] = {
            "host": self.host,
            "port": self.port,
            "protocol_version": proto_version,
            "software_version": software_version,
            "comment": comment,
            "raw_banner": banner_str,
            "banner_latency_ms": round(elapsed_ms, 2),
        }

        return status, msg, elapsed_ms, details, remediation

    def _check_ssh_auth_cli(self) -> Tuple[CheckStatus, str, Optional[float], Dict[str, Any], Optional[str]]:
        """Test SSH batch mode authentication using ssh CLI client."""
        cmd = [
            "ssh",
            "-o", "BatchMode=yes",
            "-o", f"ConnectTimeout={int(self.timeout)}",
            "-o", "StrictHostKeyChecking=accept-new",
            "-p", str(self.port),
        ]
        if self.key_path:
            cmd.extend(["-i", self.key_path])

        target = f"{self.username}@{self.host}" if self.username else self.host
        cmd.extend([target, "echo MEDIC_SSH_OK"])

        start_time = time.perf_counter()
        try:
            proc = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                timeout=self.timeout + 2.0,
            )
            elapsed_ms = (time.perf_counter() - start_time) * 1000.0

            if proc.returncode == 0 and "MEDIC_SSH_OK" in proc.stdout:
                return (
                    CheckStatus.PASSED,
                    f"SSH authenticated successfully to {target} in {elapsed_ms:.1f}ms",
                    elapsed_ms,
                    {"authenticated": True},
                    None,
                )
            elif proc.returncode == 255:
                err_msg = proc.stderr.strip() or "Permission denied (publickey)"
                return (
                    CheckStatus.FAILED,
                    f"SSH authentication failed: {err_msg}",
                    elapsed_ms,
                    {"exit_code": proc.returncode, "stderr": proc.stderr},
                    f"Ensure public key is added to ~/.ssh/authorized_keys on {self.host}.",
                )
            else:
                return (
                    CheckStatus.WARNING,
                    f"SSH command exited with code {proc.returncode}: {proc.stderr.strip()}",
                    elapsed_ms,
                    {"exit_code": proc.returncode, "stderr": proc.stderr},
                    "Check user shell and permissions.",
                )
        except subprocess.TimeoutExpired:
            elapsed_ms = (time.perf_counter() - start_time) * 1000.0
            return (
                CheckStatus.FAILED,
                f"SSH authentication timed out after {self.timeout:.1f}s",
                elapsed_ms,
                {},
                "Check network latency or SSH banner delays.",
            )
        except Exception as e:
            elapsed_ms = (time.perf_counter() - start_time) * 1000.0
            return (
                CheckStatus.ERROR,
                f"Failed to execute ssh command: {e}",
                elapsed_ms,
                {},
                None,
            )

    def run(self) -> CheckResult:
        if self.check_auth and (self.username or self.key_path):
            status, msg, latency_ms, details, remediation = self._check_ssh_auth_cli()
        else:
            status, msg, latency_ms, details, remediation = self._check_socket_banner()

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target=f"{self.host}:{self.port}",
            latency_ms=latency_ms,
            details=details,
            remediation=remediation,
        )
