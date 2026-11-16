"""Command Line Interface for medic health check & diagnostic tool."""

import argparse
import sys
import time
from typing import Any, Dict, List, Optional, Sequence

import argcomplete
from rich.console import Console

from medic import __version__, completion
from medic.checks.dns import DNSCheck
from medic.checks.exec import ExecCheck
from medic.checks.http import HTTPCheck
from medic.checks.ping import PingCheck
from medic.checks.ssh import SSHCheck
from medic.checks.system import (
    SystemCheckGroup,
)
from medic.checks.tcp import TCPCheck
from medic.checks.tls import TLSCheck
from medic.config import ConfigError, load_config
from medic.core.base import BaseCheck
from medic.core.enums import CheckStatus, OutputFormat
from medic.core.models import MedicReport
from medic.core.runner import MedicRunner
from medic.formatters import get_formatter


def parse_threshold(val_str: str, default: tuple[float, float]) -> tuple[float, float]:
    """Parse comma-separated threshold string like '80,95'."""
    try:
        parts = [float(p.strip()) for p in val_str.split(",")]
        if len(parts) == 1:
            return parts[0], default[1]
        return parts[0], parts[1]
    except Exception:
        return default


def parse_status_codes(val_str: str) -> List[int]:
    """Parse comma-separated status codes or ranges like '200,201,200-299'."""
    codes: List[int] = []
    for part in val_str.split(","):
        part = part.strip()
        if "-" in part:
            try:
                start, end = part.split("-", 1)
                codes.extend(range(int(start), int(end) + 1))
            except ValueError:
                pass
        else:
            try:
                codes.append(int(part))
            except ValueError:
                pass
    return codes if codes else [200, 201, 204, 301, 302, 307, 308]


def build_parser() -> argparse.ArgumentParser:
    """Create the argument parser with all CLI flags."""
    parser = argparse.ArgumentParser(
        prog="medic",
        description="🩺 medic: Fast, type-safe CLI health check & diagnostic tool",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  medic                                     # Run default system & connectivity checkup
  medic --all                               # Run full default checkup suite
  medic --system                            # Check CPU, RAM, Swap, Root Disk, and Uptime
  medic --ping 1.1.1.1 8.8.8.8 google.com  # Ping test multiple hosts
  medic --tcp localhost:5432 redis:6379     # Test TCP ports connectivity
  medic --http https://github.com           # Check HTTP endpoint availability
  medic --tls google.com github.com:443     # Verify SSL/TLS cert expiration & SANs
  medic --ssh 192.168.1.50:22               # Check SSH daemon & handshake
  medic --system --ping 1.1.1.1 --format json # Output results as JSON
  medic --config medic.toml                 # Run checks declared in a TOML file (supports stages)
  medic --exec "pg_isready -h db" --dns example.com
  medic --system --watch 5                  # Live monitor, refresh every 5s
  medic --config medic.toml --format junit --fail-on warning   # CI mode
        """,
    )

    parser.add_argument("-V", "--version", action="version", version=f"medic {__version__}")

    comp_group = parser.add_argument_group("Shell Completion")
    comp_group.add_argument(
        "--install-completion",
        action="store_true",
        help="Install tab-completion for your current shell (no rc file edits needed)",
    )
    comp_group.add_argument(
        "--completion",
        dest="print_completion",
        choices=completion.SHELLS,
        metavar="SHELL",
        help="Print the completion script for bash, zsh or fish",
    )
    # Check Targets
    targets_group = parser.add_argument_group("Check Targets & Flags")
    targets_group.add_argument(
        "-a",
        "--all",
        action="store_true",
        help="Run comprehensive default check suite (system, default gateway/DNS, internet reachability)",
    )
    targets_group.add_argument(
        "-s",
        "--system",
        action="store_true",
        help="Run system resources checks (CPU, Memory, Swap, Disk, Uptime)",
    )
    targets_group.add_argument(
        "--disk",
        action="append",
        dest="disks",
        metavar="PATH",
        help="Specific disk mount/path to check (can be specified multiple times, e.g. --disk / --disk /var)",
    )
    targets_group.add_argument(
        "--cpu-threshold",
        default="80,95",
        metavar="WARN,CRIT",
        help="CPU warning and critical thresholds in percent (default: 80,95)",
    )
    targets_group.add_argument(
        "--ram-threshold",
        default="85,95",
        metavar="WARN,CRIT",
        help="RAM warning and critical thresholds in percent (default: 85,95)",
    )
    targets_group.add_argument(
        "--disk-threshold",
        default="85,92",
        metavar="WARN,CRIT",
        help="Disk warning and critical thresholds in percent (default: 85,92)",
    )
    targets_group.add_argument(
        "--ping",
        nargs="+",
        metavar="HOST",
        help="Host(s) or IP(s) to ping with ICMP (e.g. --ping 1.1.1.1 google.com)",
    )
    targets_group.add_argument(
        "--ping-count",
        type=int,
        default=3,
        metavar="N",
        help="Number of ICMP ping packets to transmit (default: 3)",
    )
    targets_group.add_argument(
        "--tcp",
        nargs="+",
        metavar="HOST:PORT",
        help="TCP host:port target(s) to test (e.g. --tcp localhost:5432 1.1.1.1:53)",
    )
    targets_group.add_argument(
        "--http",
        nargs="+",
        metavar="URL",
        help="HTTP/HTTPS URL(s) to check (e.g. --http https://github.com http://localhost:8000)",
    )
    targets_group.add_argument(
        "--http-method",
        default="GET",
        metavar="METHOD",
        help="HTTP method to use (GET, HEAD, POST; default: GET)",
    )
    targets_group.add_argument(
        "--http-status",
        default="200,201,204,301,302,307,308",
        metavar="CODES",
        help="Expected HTTP status codes or ranges (e.g. 200, 200-299; default: 2xx/3xx)",
    )
    targets_group.add_argument(
        "--http-match",
        metavar="TEXT",
        help="Verify response body contains expected substring",
    )
    targets_group.add_argument(
        "--no-verify-ssl",
        action="store_true",
        help="Disable SSL/TLS certificate verification for HTTP checks",
    )
    targets_group.add_argument(
        "--tls",
        nargs="+",
        metavar="HOST[:PORT]",
        help="TLS/SSL certificate check targets (default port 443; e.g. --tls google.com:443)",
    )
    targets_group.add_argument(
        "--tls-warn-days",
        type=int,
        default=30,
        metavar="DAYS",
        help="Days before cert expiration to trigger warning (default: 30)",
    )
    targets_group.add_argument(
        "--tls-crit-days",
        type=int,
        default=7,
        metavar="DAYS",
        help="Days before cert expiration to trigger critical failure (default: 7)",
    )
    targets_group.add_argument(
        "--ssh",
        nargs="+",
        metavar="HOST[:PORT]",
        help="SSH target(s) to check service & banner (e.g. --ssh localhost:22 192.168.1.10)",
    )
    targets_group.add_argument(
        "--ssh-user",
        metavar="USER",
        help="SSH username for authentication check",
    )
    targets_group.add_argument(
        "--ssh-key",
        metavar="KEY_FILE",
        help="SSH private key path for authentication check",
    )
    targets_group.add_argument(
        "--ssh-auth",
        action="store_true",
        help="Test full SSH authentication using local OpenSSH client",
    )
    targets_group.add_argument(
        "--dns",
        nargs="+",
        metavar="HOST",
        help="Hostname(s) to resolve via the system resolver",
    )
    targets_group.add_argument(
        "--exec",
        action="append",
        dest="exec_cmds",
        metavar="CMD",
        help="Command to run (no shell); passes on exit code 0. Repeatable, e.g. --exec 'pg_isready -h db'",
    )
    targets_group.add_argument(
        "-c",
        "--config",
        metavar="FILE",
        help="TOML file declaring checks, stages and settings (CLI flags override [settings])",
    )

    # Runner & Concurrency
    runner_group = parser.add_argument_group("Execution & Performance")
    runner_group.add_argument(
        "--timeout",
        type=float,
        default=None,
        metavar="SEC",
        help="Per-check timeout in seconds (default: 6.0)",
    )
    runner_group.add_argument(
        "-j",
        "--concurrency",
        type=int,
        default=None,
        metavar="N",
        help="Number of concurrent worker threads (default: 8)",
    )
    runner_group.add_argument(
        "--no-parallel",
        action="store_true",
        help="Run checks sequentially instead of parallel threads",
    )
    runner_group.add_argument(
        "--stop-on-failure",
        action="store_true",
        help="Stop running remaining checks on first failure (runs sequentially)",
    )
    runner_group.add_argument(
        "--fail-on",
        choices=["failure", "warning"],
        default=None,
        help="Exit non-zero on failures only (default) or also on warnings",
    )
    runner_group.add_argument(
        "-w",
        "--watch",
        nargs="?",
        type=float,
        const=5.0,
        default=None,
        metavar="SEC",
        help="Re-run continuously every SEC seconds (default 5) until Ctrl-C",
    )

    # Output Formatting
    out_group = parser.add_argument_group("Output Formatting")
    out_group.add_argument(
        "-f",
        "--format",
        choices=["rich", "json", "text", "compact", "junit"],
        default="rich",
        help="Output format (default: rich)",
    )
    out_group.add_argument(
        "-v",
        "--verbose",
        action="store_true",
        help="Show detailed diagnostic breakdowns and metrics",
    )
    out_group.add_argument(
        "-q",
        "--quiet",
        action="store_true",
        help="Quiet mode: minimize console output, rely on exit code",
    )
    out_group.add_argument(
        "--no-color",
        action="store_true",
        help="Disable colored output",
    )
    out_group.add_argument(
        "--no-remediation",
        action="store_true",
        help="Hide remediation hints panel",
    )

    return parser


def build_checks_from_args(args: argparse.Namespace, default_suite: bool = True) -> List[BaseCheck]:
    """Assemble list of check instances based on CLI flags."""
    if args.timeout is None:
        args.timeout = 6.0
    checks: List[BaseCheck] = []

    cpu_warn, cpu_crit = parse_threshold(args.cpu_threshold, (80.0, 95.0))
    ram_warn, ram_crit = parse_threshold(args.ram_threshold, (85.0, 95.0))
    disk_warn, disk_crit = parse_threshold(args.disk_threshold, (85.0, 92.0))

    disks = args.disks or ["/"]

    has_specific_check = any(
        [
            args.system,
            args.disks,
            args.ping,
            args.tcp,
            args.http,
            args.tls,
            args.ssh,
            args.dns,
            args.exec_cmds,
        ]
    )

    # --all adds internet reachability on top of the local suite. With no flags
    # at all, run only local system checks so the default never needs network.
    if args.all or (default_suite and not has_specific_check):
        checks.append(
            SystemCheckGroup(
                disk_paths=disks,
                cpu_warn=cpu_warn,
                cpu_crit=cpu_crit,
                ram_warn=ram_warn,
                ram_crit=ram_crit,
                disk_warn=disk_warn,
                disk_crit=disk_crit,
                timeout=args.timeout,
            )
        )
        if args.all:
            checks.append(PingCheck(host="1.1.1.1", name="Ping (Cloudflare DNS 1.1.1.1)", count=args.ping_count, timeout_sec=args.timeout))
            checks.append(PingCheck(host="8.8.8.8", name="Ping (Google DNS 8.8.8.8)", count=args.ping_count, timeout_sec=args.timeout))
            checks.append(HTTPCheck(url="https://cloudflare.com", name="HTTP (Cloudflare)", timeout=args.timeout))
            checks.append(TLSCheck(host="cloudflare.com", port=443, name="TLS Cert (cloudflare.com)", warn_days=args.tls_warn_days, crit_days=args.tls_crit_days, timeout=args.timeout))
        return checks

    # Specific system checks
    if args.system or args.disks:
        checks.append(
            SystemCheckGroup(
                disk_paths=disks,
                cpu_warn=cpu_warn,
                cpu_crit=cpu_crit,
                ram_warn=ram_warn,
                ram_crit=ram_crit,
                disk_warn=disk_warn,
                disk_crit=disk_crit,
                timeout=args.timeout,
            )
        )

    # Ping checks
    if args.ping:
        for host in args.ping:
            checks.append(
                PingCheck(
                    host=host,
                    count=args.ping_count,
                    timeout_sec=args.timeout,
                )
            )

    # TCP checks
    if args.tcp:
        for target in args.tcp:
            checks.append(
                TCPCheck.from_target(
                    target=target,
                    timeout=args.timeout,
                )
            )

    # HTTP checks
    if args.http:
        expected_status = parse_status_codes(args.http_status)
        for url in args.http:
            checks.append(
                HTTPCheck(
                    url=url,
                    method=args.http_method,
                    expected_status=expected_status,
                    verify_ssl=not args.no_verify_ssl,
                    contains_text=args.http_match,
                    timeout=args.timeout,
                )
            )

    # TLS checks
    if args.tls:
        for target in args.tls:
            checks.append(
                TLSCheck.from_target(
                    target=target,
                    warn_days=args.tls_warn_days,
                    crit_days=args.tls_crit_days,
                    timeout=args.timeout,
                )
            )

    # SSH checks
    if args.ssh:
        for target in args.ssh:
            checks.append(
                SSHCheck.from_target(
                    target=target,
                    username=args.ssh_user,
                    key_path=args.ssh_key,
                    check_auth=args.ssh_auth,
                    timeout=args.timeout,
                )
            )

    if args.dns:
        for host in args.dns:
            checks.append(DNSCheck(host=host, timeout=args.timeout))

    if args.exec_cmds:
        for cmd in args.exec_cmds:
            checks.append(ExecCheck(command=cmd, timeout=args.timeout))

    return checks


def _status_changes(prev: Dict[str, CheckStatus], report: MedicReport) -> List[str]:
    """Describe per-check status transitions since the previous watch iteration."""
    lines = []
    for r in report.results:
        key = f"{r.name}|{r.target}"
        old = prev.get(key)
        if old is not None and old != r.status:
            lines.append(f"{r.name}: {old.value} -> {r.status.value} ({r.message})")
    return lines


def _watch(runner: MedicRunner, args: argparse.Namespace, fail_on: str) -> int:
    """Re-run the checks on an interval, rendering each report; Ctrl-C exits."""
    interval = max(args.watch, 0.5)
    fmt = OutputFormat(args.format)
    console = Console(no_color=args.no_color)
    live_screen = fmt == OutputFormat.RICH and console.is_terminal and not args.quiet
    formatter = get_formatter(
        fmt=fmt,
        console=console,
        verbose=args.verbose,
        show_remediation=not args.no_remediation,
        no_color=args.no_color,
    )
    prev: Dict[str, CheckStatus] = {}
    code = 0
    try:
        while True:
            report = runner.run()
            code = report.exit_code_for(fail_on)
            if live_screen:
                console.clear()
            if not args.quiet:
                formatter.render(report)
            for line in _status_changes(prev, report):
                print(f"[change] {line}", file=sys.stderr)
            prev = {f"{r.name}|{r.target}": r.status for r in report.results}
            if live_screen:
                console.print(f"[dim]Refreshing every {interval:g}s - Ctrl-C to quit[/dim]")
            time.sleep(interval)
    except KeyboardInterrupt:
        return code


def main(argv: Optional[Sequence[str]] = None) -> int:
    """Main CLI entry point."""
    parser = build_parser()
    argcomplete.autocomplete(parser)
    args = parser.parse_args(argv)

    if args.print_completion:
        print(completion.script_for(args.print_completion))
        return 0
    if args.install_completion:
        shell = completion.detect_shell()
        if shell is None:
            print("medic: cannot detect shell from $SHELL; use --completion {bash,zsh,fish}", file=sys.stderr)
            return 2
        path, note = completion.install(shell)
        print(f"Installed {shell} completion: {path}")
        print(note or "Open a new shell (or run `exec $SHELL`) to activate.")
        return 0

    settings: Dict[str, Any] = {}
    config_checks: List[BaseCheck] = []
    if args.config:
        try:
            settings, config_checks = load_config(args.config, args.timeout)
        except ConfigError as exc:
            print(f"medic: config error: {exc}", file=sys.stderr)
            return 2

    # Precedence: explicit CLI flag > config [settings] > built-in default.
    if args.timeout is None:
        args.timeout = float(settings.get("timeout", 6.0))
    concurrency = args.concurrency if args.concurrency is not None else int(settings.get("concurrency", 8))
    fail_on = args.fail_on or settings.get("fail_on", "failure")
    stop_on_failure = args.stop_on_failure or bool(settings.get("stop_on_failure", False))
    parallel = not args.no_parallel and bool(settings.get("parallel", True))

    checks = config_checks + build_checks_from_args(args, default_suite=not args.config)
    if not checks:
        if not args.quiet:
            print("No checks configured to run.", file=sys.stderr)
        return 1

    runner = MedicRunner(
        checks=checks,
        parallel=parallel,
        max_workers=concurrency,
        stop_on_failure=stop_on_failure,
    )

    if args.watch is not None:
        return _watch(runner, args, fail_on)

    report: MedicReport = runner.run()

    if not args.quiet:
        fmt = OutputFormat(args.format)
        formatter = get_formatter(
            fmt=fmt,
            verbose=args.verbose,
            show_remediation=not args.no_remediation,
            no_color=args.no_color,
        )
        formatter.render(report)

    return report.exit_code_for(fail_on)


if __name__ == "__main__":
    sys.exit(main())
