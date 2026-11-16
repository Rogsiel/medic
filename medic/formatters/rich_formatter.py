"""Rich-based colorful and structured terminal output formatter."""

from typing import Optional

from rich.box import ROUNDED, SIMPLE_HEAVY
from rich.console import Console, Group
from rich.panel import Panel
from rich.table import Table
from rich.text import Text

from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import MedicReport

from .base import BaseFormatter


class RichFormatter(BaseFormatter):
    """Renders MedicReports with rich formatting, tables, status colors, and panels."""

    def __init__(
        self,
        console: Optional[Console] = None,
        verbose: bool = False,
        show_remediation: bool = True,
        no_color: bool = False,
    ) -> None:
        self.console = console or Console(no_color=no_color)
        self.verbose = verbose
        self.show_remediation = show_remediation

    def _category_style(self, category: CheckCategory) -> str:
        return {
            CheckCategory.SYSTEM: "cyan",
            CheckCategory.PING: "blue",
            CheckCategory.TCP: "magenta",
            CheckCategory.HTTP: "green",
            CheckCategory.TLS: "bright_magenta",
            CheckCategory.SSH: "yellow",
            CheckCategory.DOCKER: "bright_blue",
            CheckCategory.DNS: "bright_cyan",
            CheckCategory.EXEC: "bright_white",
        }.get(category, "white")

    def _status_styled_text(self, status: CheckStatus) -> Text:
        icons = {
            CheckStatus.PASSED: "✔ PASS",
            CheckStatus.WARNING: "⚠ WARN",
            CheckStatus.FAILED: "✖ FAIL",
            CheckStatus.SKIPPED: "⊘ SKIP",
            CheckStatus.ERROR: "⚡ ERR",
        }
        styles = {
            CheckStatus.PASSED: "bold green",
            CheckStatus.WARNING: "bold yellow",
            CheckStatus.FAILED: "bold red",
            CheckStatus.SKIPPED: "dim",
            CheckStatus.ERROR: "bold red on white",
        }
        return Text(icons.get(status, status.value), style=styles.get(status, "white"))

    def render(self, report: MedicReport) -> None:
        console = self.console

        # Header Title
        title_text = "🩺 [bold white]MEDIC[/bold white] Health Diagnostic"
        subtitle = f"[dim]Executed {report.total_count} check{'s' if report.total_count != 1 else ''} in {report.duration_ms:.1f}ms • {report.timestamp.strftime('%Y-%m-%d %H:%M:%S UTC')}[/dim]"

        console.print()
        console.print(Panel(f"{title_text}\n{subtitle}", box=ROUNDED, border_style="cyan", padding=(0, 2)))
        console.print()

        if not report.results:
            console.print("[yellow]No checks were executed. Specify flags like --all, --system, --http, --ping, etc.[/yellow]\n")
            return

        # Main Results Table
        table = Table(
            box=ROUNDED,
            header_style="bold bright_white",
            show_header=True,
            expand=True,
            padding=(0, 1),
        )

        table.add_column("Status", justify="center", width=9)
        table.add_column("Category", justify="left", width=10)
        table.add_column("Check Name", justify="left", style="bold", min_width=18)
        table.add_column("Latency", justify="right", width=11)
        table.add_column("Details / Output", justify="left", ratio=1)

        for res in report.results:
            cat_style = self._category_style(res.category)
            cat_badge = Text(res.category.display_name, style=f"bold {cat_style}")
            status_text = self._status_styled_text(res.status)

            lat_str = f"{res.latency_ms:.1f} ms" if res.latency_ms is not None else "—"
            lat_style = "green" if res.latency_ms and res.latency_ms < 150 else ("yellow" if res.latency_ms and res.latency_ms < 600 else "dim")
            latency_text = Text(lat_str, style=lat_style)

            msg_style = "white" if res.status == CheckStatus.PASSED else ("yellow" if res.status == CheckStatus.WARNING else "red")
            message_text = Text(res.message, style=msg_style)

            table.add_row(
                status_text,
                cat_badge,
                res.name,
                latency_text,
                message_text,
            )

        console.print(table)
        console.print()

        # Detailed Breakdown for Failures / Warnings or when Verbose
        issues = [r for r in report.results if r.status in (CheckStatus.WARNING, CheckStatus.FAILED, CheckStatus.ERROR)]
        if issues and self.show_remediation:
            remediation_items = []
            for issue in issues:
                icon = "✖" if issue.status in (CheckStatus.FAILED, CheckStatus.ERROR) else "⚠"
                color = "red" if issue.status in (CheckStatus.FAILED, CheckStatus.ERROR) else "yellow"
                header = f"[{color}]{icon} {issue.name}[/{color}] ({issue.target or issue.category.value})"
                desc = f"  [dim]Issue:[/dim] {issue.message}"

                fix_hint = ""
                if issue.remediation:
                    fix_hint = f"\n  [bold cyan]Remediation:[/bold cyan] {issue.remediation}"
                elif issue.details.get("exception"):
                    fix_hint = f"\n  [dim red]Error:[/dim red] {issue.details.get('error')}"

                remediation_items.append(f"{header}\n{desc}{fix_hint}")

            panel_content = "\n\n".join(remediation_items)
            console.print(
                Panel(
                    panel_content,
                    title="[bold yellow]Action Items & Remediation Hints[/bold yellow]",
                    box=ROUNDED,
                    border_style="yellow",
                    padding=(1, 2),
                )
            )
            console.print()

        # Verbose details dump
        if self.verbose:
            verbose_tables = []
            for res in report.results:
                if res.details:
                    det_table = Table(title=f"Details: {res.name}", box=SIMPLE_HEAVY, show_header=False)
                    det_table.add_column("Key", style="cyan")
                    det_table.add_column("Value", style="white")
                    for k, v in res.details.items():
                        if k not in ("traceback", "raw_output", "headers") or self.verbose:
                            val_str = str(v)
                            if len(val_str) > 120 and not self.verbose:
                                val_str = val_str[:120] + "..."
                            det_table.add_row(k, val_str)
                    verbose_tables.append(det_table)

            if verbose_tables:
                console.print(Panel(Group(*verbose_tables), title="[bold blue]Diagnostic Details[/bold blue]", box=ROUNDED))
                console.print()

        # Summary Bar
        summary_parts = [
            f"[bold green]✔ {report.passed_count} passed[/bold green]",
        ]
        if report.warning_count > 0:
            summary_parts.append(f"[bold yellow]⚠ {report.warning_count} warning{'s' if report.warning_count != 1 else ''}[/bold yellow]")
        if report.failed_count > 0:
            summary_parts.append(f"[bold red]✖ {report.failed_count} failed[/bold red]")
        if report.error_count > 0:
            summary_parts.append(f"[bold magenta]⚡ {report.error_count} error{'s' if report.error_count != 1 else ''}[/bold magenta]")
        if report.skipped_count > 0:
            summary_parts.append(f"[dim]⊘ {report.skipped_count} skipped[/dim]")

        summary_text = "   ".join(summary_parts)

        if report.is_healthy:
            status_banner = "[bold white on green]  ALL SYSTEMS HEALTHY  [/bold white on green]"
        elif report.failed_count == 0 and report.error_count == 0:
            status_banner = "[bold black on yellow]  WARNINGS DETECTED  [/bold black on yellow]"
        else:
            status_banner = "[bold white on red]  HEALTH CHECKS FAILED  [/bold white on red]"

        console.print(f"{status_banner}   {summary_text}   [dim]({report.duration_ms:.1f}ms total)[/dim]")
        console.print()
