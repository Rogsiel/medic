"""Regenerate docs/screenshot.svg from a deterministic demo run (no internet needed)."""

import io
import sys
from pathlib import Path

from rich.console import Console

from medic.checks.dns import DNSCheck
from medic.checks.exec import ExecCheck
from medic.checks.system import SystemCheckGroup
from medic.checks.tcp import TCPCheck
from medic.core.runner import MedicRunner
from medic.formatters.rich_formatter import RichFormatter

checks = [
    SystemCheckGroup(disk_paths=["/"]),
    DNSCheck("localhost", name="DNS localhost"),
    ExecCheck([sys.executable, "-c", "print('ok')"], name="Migrations applied", contains_text="ok"),
    TCPCheck("127.0.0.1", 1, name="TCP db:5432", timeout=1),
    ExecCheck([sys.executable, "-c", "print('ok')"], name="API smoke test"),
]
checks[-1].stage = 1  # skipped because the TCP check in stage 0 fails
checks[3].stage = 0

console = Console(record=True, width=110, force_terminal=True, color_system="truecolor", file=io.StringIO())
RichFormatter(console=console).render(MedicRunner(checks=checks).run())
out = Path(__file__).resolve().parent.parent / "docs" / "screenshot.svg"
console.save_svg(str(out), title="medic")
print(f"wrote {out}")
