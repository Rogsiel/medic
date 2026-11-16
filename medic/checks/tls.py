"""TLS/SSL certificate and handshake health checks."""

import fnmatch
import socket
import ssl
import time
from datetime import datetime, timezone
from typing import Any, Dict, List, Optional

try:
    from cryptography import x509
    from cryptography.hazmat.backends import default_backend
    from cryptography.x509.oid import NameOID
    HAS_CRYPTOGRAPHY = True
except ImportError:
    HAS_CRYPTOGRAPHY = False

from medic.checks.tcp import parse_host_port
from medic.core.base import BaseCheck, timed_execution
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult


class TLSCheck(BaseCheck):
    """Checks TLS/SSL certificate validity, expiration, issuer, SANs, and protocol version."""

    category = CheckCategory.TLS

    def __init__(
        self,
        host: str,
        port: int = 443,
        name: Optional[str] = None,
        warn_days: int = 30,
        crit_days: int = 7,
        sni_hostname: Optional[str] = None,
        check_hostname: bool = True,
        timeout: float = 8.0,
        enabled: bool = True,
    ) -> None:
        chk_name = name or f"TLS {host}:{port}"
        super().__init__(name=chk_name, timeout=timeout, enabled=enabled)
        self.host = host.strip()
        self.port = port
        self.warn_days = warn_days
        self.crit_days = crit_days
        self.sni_hostname = sni_hostname or self.host
        self.check_hostname = check_hostname

    @classmethod
    def from_target(cls, target: str, default_port: int = 443, **kwargs: Any) -> "TLSCheck":
        """Factory method to instantiate from 'host:port' string."""
        host, port = parse_host_port(target, default_port=default_port)
        return cls(host=host, port=port, **kwargs)

    def _matches_san(self, hostname: str, sans: List[str]) -> bool:
        """Check if hostname matches any Subject Alternative Name."""
        h_lower = hostname.lower()
        for san in sans:
            s_lower = san.lower()
            if s_lower == h_lower:
                return True
            if s_lower.startswith("*."):
                if fnmatch.fnmatch(h_lower, s_lower):
                    return True
        return False

    def run(self) -> CheckResult:
        target_str = f"{self.host}:{self.port}"
        with timed_execution() as timer:
            # Create SSL Context
            ctx = ssl.create_default_context()
            if not self.check_hostname:
                ctx.check_hostname = False
                ctx.verify_mode = ssl.CERT_NONE

            ssock: Optional[ssl.SSLSocket] = None

            try:
                raw_sock = socket.create_connection((self.host, self.port), timeout=self.timeout)
                handshake_start = time.perf_counter()
                ssock = ctx.wrap_socket(raw_sock, server_hostname=self.sni_hostname)
                handshake_ms = (time.perf_counter() - handshake_start) * 1000.0

                cipher_info = ssock.cipher()
                tls_version = ssock.version()
                der_cert = ssock.getpeercert(binary_form=True)
                dict_cert = ssock.getpeercert(binary_form=False)

            except ssl.SSLCertVerificationError as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"TLS certificate verification error: {e.verify_message or str(e)}",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                    remediation="Verify certificate trust chain, issuer CA, or hostname mismatch.",
                )
            except ssl.SSLError as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"TLS handshake failed: {e}",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                    remediation=f"Ensure server at {target_str} supports TLS and acceptable cipher suites.",
                )
            except TimeoutError:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Connection timed out after {self.timeout:.1f}s",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                    remediation=f"Check firewall or network routing for port {self.port}.",
                )
            except Exception as e:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"TLS connection error: {e}",
                    target=target_str,
                    latency_ms=timer["elapsed_ms"],
                )
            finally:
                if ssock is not None:
                    try:
                        ssock.close()
                    except Exception:
                        pass

            # Extract certificate details
            now_utc = datetime.now(timezone.utc)
            not_after_utc: Optional[datetime] = None
            not_before_utc: Optional[datetime] = None
            issuer_cn = "Unknown"
            subject_cn = "Unknown"
            sans: List[str] = []
            serial_number: Optional[str] = None

            if HAS_CRYPTOGRAPHY and der_cert:
                try:
                    cert = x509.load_der_x509_certificate(der_cert, default_backend())
                    try:
                        not_after_utc = cert.not_valid_after_utc
                        not_before_utc = cert.not_valid_before_utc
                    except AttributeError:
                        # Fallback for older cryptography versions
                        not_after_utc = cert.not_valid_after.replace(tzinfo=timezone.utc)
                        not_before_utc = cert.not_valid_before.replace(tzinfo=timezone.utc)

                    serial_number = hex(cert.serial_number)

                    # Issuer CN
                    issuers = cert.issuer.get_attributes_for_oid(NameOID.COMMON_NAME)
                    if issuers:
                        issuer_cn = str(issuers[0].value)
                    else:
                        orgs = cert.issuer.get_attributes_for_oid(NameOID.ORGANIZATION_NAME)
                        if orgs:
                            issuer_cn = str(orgs[0].value)

                    # Subject CN
                    subjects = cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)
                    if subjects:
                        subject_cn = str(subjects[0].value)

                    # SANs
                    try:
                        san_ext = cert.extensions.get_extension_for_class(x509.SubjectAlternativeName)
                        sans = [str(name) for name in san_ext.value.get_values_for_type(x509.DNSName)]
                    except Exception:
                        sans = [subject_cn] if subject_cn != "Unknown" else []
                except Exception:
                    pass

            # Fallback to ssl dict_cert if cryptography extraction was unavailable or incomplete
            if not_after_utc is None and dict_cert:
                na_str = dict_cert.get("notAfter")
                if na_str:
                    try:
                        not_after_utc = datetime.strptime(
                            str(na_str), "%b %d %H:%M:%S %Y %Z"
                        ).replace(tzinfo=timezone.utc)
                    except Exception:
                        pass
                for item in dict_cert.get("subjectAltName", []):
                    if item[0] == "DNS":
                        sans.append(str(item[1]))

            # Calculate days remaining
            days_remaining: Optional[int] = None
            if not_after_utc:
                delta = not_after_utc - now_utc
                days_remaining = delta.days

            # Determine check status
            status = CheckStatus.PASSED
            remediation = None

            if days_remaining is not None:
                if days_remaining < 0:
                    status = CheckStatus.FAILED
                    remediation = f"Certificate expired {abs(days_remaining)} days ago! Renew certificate immediately via certbot or ACME provider."
                elif days_remaining <= self.crit_days:
                    status = CheckStatus.FAILED
                    remediation = f"Certificate expires in {days_remaining} days (critical threshold: {self.crit_days}d). Renew certificate now."
                elif days_remaining <= self.warn_days:
                    status = CheckStatus.WARNING
                    remediation = f"Certificate expires in {days_remaining} days (warning threshold: {self.warn_days}d). Plan renewal soon."
            else:
                status = CheckStatus.WARNING
                remediation = "Could not parse certificate expiration date."

            # Verify SAN match
            if self.check_hostname and sans and not self._matches_san(self.sni_hostname, sans):
                status = CheckStatus.FAILED
                remediation = f"Hostname '{self.sni_hostname}' is not covered by certificate SANs: {sans[:5]}"

            cipher_name = cipher_info[0] if cipher_info else "Unknown"
            days_text = f"{days_remaining} days left" if days_remaining is not None else "unknown expiry"
            expiry_date_str = not_after_utc.strftime("%Y-%m-%d") if not_after_utc else "Unknown"

            msg = f"TLS Valid ({days_text}, expires: {expiry_date_str}, issuer: {issuer_cn}, {tls_version})"

            details: Dict[str, Any] = {
                "host": self.host,
                "port": self.port,
                "sni_hostname": self.sni_hostname,
                "tls_version": tls_version,
                "cipher": cipher_name,
                "issuer": issuer_cn,
                "subject": subject_cn,
                "days_remaining": days_remaining,
                "expires_at": not_after_utc.isoformat() if not_after_utc else None,
                "valid_from": not_before_utc.isoformat() if not_before_utc else None,
                "sans": sans,
                "serial_number": serial_number,
                "handshake_ms": round(handshake_ms, 2),
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target=target_str,
            latency_ms=handshake_ms,
            details=details,
            remediation=remediation,
        )
