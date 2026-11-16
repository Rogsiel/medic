"""ICMP ping reachability and latency health checks."""

import platform
import re
import shutil
import subprocess
from typing import Any, Dict, Optional

from medic.core.base import BaseCheck, timed_execution
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult


class PingCheck(BaseCheck):
    """Performs ICMP ping check to assess network reachability, packet loss, and latency."""

    category = CheckCategory.PING

    def __init__(
        self,
        host: str,
        name: Optional[str] = None,
        count: int = 3,
        timeout_sec: float = 2.0,
        warn_latency_ms: float = 150.0,
        crit_latency_ms: float = 500.0,
        warn_loss_pct: float = 0.0,
        enabled: bool = True,
    ) -> None:
        chk_name = name or f"Ping {host}"
        super().__init__(name=chk_name, timeout=timeout_sec * count + 2.0, enabled=enabled)
        self.host = host.strip()
        self.count = max(1, count)
        self.timeout_sec = max(0.5, timeout_sec)
        self.warn_latency_ms = warn_latency_ms
        self.crit_latency_ms = crit_latency_ms
        self.warn_loss_pct = warn_loss_pct

    def _build_command(self) -> list[str]:
        """Construct the ping CLI command for the current operating system."""
        sys_name = platform.system().lower()
        ping_bin = shutil.which("ping") or "ping"

        if sys_name == "windows":
            # -n count, -w timeout in ms
            return [ping_bin, "-n", str(self.count), "-w", str(int(self.timeout_sec * 1000)), self.host]
        elif sys_name == "darwin":
            # macOS: -c count, -W timeout in ms (or -t in secs)
            return [ping_bin, "-c", str(self.count), "-W", str(int(self.timeout_sec * 1000)), self.host]
        else:
            # Linux & others: -c count, -W timeout in seconds
            return [ping_bin, "-c", str(self.count), "-W", str(int(self.timeout_sec)), self.host]

    def _parse_output(self, stdout: str) -> Dict[str, Any]:
        """Parse packet loss and RTT statistics from ping output."""
        data: Dict[str, Any] = {
            "transmitted": None,
            "received": None,
            "packet_loss_pct": None,
            "min_rtt_ms": None,
            "avg_rtt_ms": None,
            "max_rtt_ms": None,
            "mdev_rtt_ms": None,
            "resolved_ip": None,
        }

        # IP resolution (e.g. PING google.com (142.250.190.46))
        ip_match = re.search(r"\(([\d\.:a-fA-F]+)\)", stdout)
        if ip_match:
            data["resolved_ip"] = ip_match.group(1)

        # Packet loss parsing
        # Matches: "3 packets transmitted, 3 received, 0% packet loss"
        loss_match = re.search(
            r"(\d+)\s+packets transmitted,\s+(\d+)\s+(?:packets\s+)?received.*?(?:([0-9\.]+)%\s+packet loss)",
            stdout,
            re.IGNORECASE,
        )
        if loss_match:
            data["transmitted"] = int(loss_match.group(1))
            data["received"] = int(loss_match.group(2))
            data["packet_loss_pct"] = float(loss_match.group(3))
        else:
            # Windows fallback: "Packets: Sent = 4, Received = 4, Lost = 0 (0% loss)"
            win_match = re.search(
                r"Sent\s*=\s*(\d+),\s*Received\s*=\s*(\d+),\s*Lost\s*=\s*\d+\s*\(([\d\.]+)%\s*loss\)",
                stdout,
                re.IGNORECASE,
            )
            if win_match:
                data["transmitted"] = int(win_match.group(1))
                data["received"] = int(win_match.group(2))
                data["packet_loss_pct"] = float(win_match.group(3))

        # Latency / RTT parsing
        # Matches: "rtt min/avg/max/mdev = 0.075/0.077/0.079/0.002 ms"
        # or "round-trip min/avg/max/stddev = 12.3/14.5/16.7/1.2 ms"
        rtt_match = re.search(
            r"(?:rtt|round-trip)\s+min/avg/max/(?:mdev|stddev)\s*=\s*([\d\.]+)/([\d\.]+)/([\d\.]+)/([\d\.]+)\s*ms",
            stdout,
            re.IGNORECASE,
        )
        if rtt_match:
            data["min_rtt_ms"] = float(rtt_match.group(1))
            data["avg_rtt_ms"] = float(rtt_match.group(2))
            data["max_rtt_ms"] = float(rtt_match.group(3))
            data["mdev_rtt_ms"] = float(rtt_match.group(4))
        else:
            # Windows fallback: "Minimum = 12ms, Maximum = 15ms, Average = 13ms"
            win_rtt = re.search(
                r"Minimum\s*=\s*(\d+)ms,\s*Maximum\s*=\s*(\d+)ms,\s*Average\s*=\s*(\d+)ms",
                stdout,
                re.IGNORECASE,
            )
            if win_rtt:
                data["min_rtt_ms"] = float(win_rtt.group(1))
                data["max_rtt_ms"] = float(win_rtt.group(2))
                data["avg_rtt_ms"] = float(win_rtt.group(3))

        return data

    def run(self) -> CheckResult:
        cmd = self._build_command()
        with timed_execution() as timer:
            try:
                proc = subprocess.run(
                    cmd,
                    capture_output=True,
                    text=True,
                    timeout=self.timeout,
                )
                stdout = proc.stdout
                parsed = self._parse_output(stdout)
            except subprocess.TimeoutExpired:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Ping timed out after {self.timeout:.1f}s for {self.host}",
                    target=self.host,
                    latency_ms=timer["elapsed_ms"],
                    remediation=f"Host {self.host} did not respond to ICMP. Check firewall/ICMP blocking or DNS resolution.",
                )
            except FileNotFoundError:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.ERROR,
                    message="ping executable not found in PATH",
                    target=self.host,
                    latency_ms=timer["elapsed_ms"],
                )
            except Exception as exc:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.ERROR,
                    message=f"Failed to execute ping: {exc}",
                    target=self.host,
                    latency_ms=timer["elapsed_ms"],
                )

            loss_pct = parsed["packet_loss_pct"]
            avg_rtt = parsed["avg_rtt_ms"]

            if loss_pct is None and proc.returncode != 0:
                return CheckResult(
                    name=self.name,
                    category=self.category,
                    status=CheckStatus.FAILED,
                    message=f"Host unreachable: {self.host}",
                    target=self.host,
                    latency_ms=timer["elapsed_ms"],
                    details={"raw_output": stdout, "exit_code": proc.returncode},
                    remediation=f"Verify network connectivity and DNS resolution for {self.host}.",
                )

            # Evaluate status
            status = CheckStatus.PASSED
            remediation = None
            latency_for_result = avg_rtt if avg_rtt is not None else timer["elapsed_ms"]

            if loss_pct is not None and loss_pct >= 100.0:
                status = CheckStatus.FAILED
                msg = f"Host unreachable (100% packet loss): {self.host}"
                remediation = f"Host {self.host} is down or ICMP packets are filtered by security groups/firewall."
            elif loss_pct is not None and loss_pct > self.warn_loss_pct:
                status = CheckStatus.WARNING
                msg = f"Packet loss: {loss_pct:.1f}% ({parsed['received']}/{parsed['transmitted']} packets) - avg: {avg_rtt or 0:.2f}ms"
                remediation = "Intermittent packet loss detected. Check for network congestion or flaky link."
            elif avg_rtt is not None and avg_rtt >= self.crit_latency_ms:
                status = CheckStatus.FAILED
                msg = f"Critical latency: {avg_rtt:.1f}ms (loss: {loss_pct or 0:.0f}%)"
                remediation = "Excessive latency detected. Investigate network routing or path MTU."
            elif avg_rtt is not None and avg_rtt >= self.warn_latency_ms:
                status = CheckStatus.WARNING
                msg = f"High latency: {avg_rtt:.1f}ms (loss: {loss_pct or 0:.0f}%)"
                remediation = f"Latency to {self.host} exceeds threshold ({self.warn_latency_ms}ms)."
            else:
                rtt_text = f"{avg_rtt:.2f}ms" if avg_rtt is not None else "OK"
                loss_text = f"loss: {loss_pct:.0f}%" if loss_pct is not None else "0% loss"
                resolved = f" ({parsed['resolved_ip']})" if parsed["resolved_ip"] else ""
                msg = f"Reachable{resolved} - RTT avg: {rtt_text}, {loss_text}"

            details: Dict[str, Any] = {
                "host": self.host,
                "resolved_ip": parsed["resolved_ip"],
                "packet_loss_pct": loss_pct,
                "min_rtt_ms": parsed["min_rtt_ms"],
                "avg_rtt_ms": parsed["avg_rtt_ms"],
                "max_rtt_ms": parsed["max_rtt_ms"],
                "mdev_rtt_ms": parsed["mdev_rtt_ms"],
                "transmitted": parsed["transmitted"],
                "received": parsed["received"],
            }

        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=msg,
            target=self.host,
            latency_ms=latency_for_result,
            details=details,
            remediation=remediation,
        )
