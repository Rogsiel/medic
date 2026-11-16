"""Run an arbitrary command and judge it by exit code and output."""

import shlex
import subprocess
import time
from typing import List, Optional, Sequence, Union

from medic.core.base import BaseCheck
from medic.core.enums import CheckCategory, CheckStatus
from medic.core.models import CheckResult

_OUTPUT_TAIL = 500


class ExecCheck(BaseCheck):
    """Passes when a command exits with the expected code (and optional output match).

    The command is run without a shell unless ``shell=True``.
    """

    category = CheckCategory.EXEC

    def __init__(
        self,
        command: Union[str, Sequence[str]],
        name: Optional[str] = None,
        expect_exit: int = 0,
        contains_text: Optional[str] = None,
        shell: bool = False,
        cwd: Optional[str] = None,
        timeout: float = 10.0,
        enabled: bool = True,
    ) -> None:
        self.argv: Union[str, List[str]]
        if isinstance(command, str):
            self.argv = command if shell else shlex.split(command)
        else:
            self.argv = list(command)
        display = command if isinstance(command, str) else shlex.join(command)
        super().__init__(name=name or f"Exec {display}", timeout=timeout, enabled=enabled)
        self.display = display
        self.expect_exit = expect_exit
        self.contains_text = contains_text
        self.shell = shell
        self.cwd = cwd

    def run(self) -> CheckResult:
        start = time.perf_counter()
        try:
            proc = subprocess.run(
                self.argv,
                shell=self.shell,
                cwd=self.cwd,
                capture_output=True,
                text=True,
                timeout=self.timeout,
            )
        except subprocess.TimeoutExpired:
            return self._result(CheckStatus.FAILED, f"Command timed out after {self.timeout:.1f}s", start)
        except (OSError, ValueError) as exc:
            return self._result(CheckStatus.FAILED, f"Could not run command: {exc}", start)

        out = (proc.stdout or "").strip()
        err = (proc.stderr or "").strip()
        details = {
            "command": self.display,
            "exit_code": proc.returncode,
            "stdout": out[-_OUTPUT_TAIL:],
            "stderr": err[-_OUTPUT_TAIL:],
        }
        if proc.returncode != self.expect_exit:
            tail = (err or out).splitlines()[-1:] or [""]
            msg = f"Exit code {proc.returncode} (expected {self.expect_exit})"
            if tail[0]:
                msg += f": {tail[0][:200]}"
            return self._result(CheckStatus.FAILED, msg, start, details)
        if self.contains_text is not None and self.contains_text not in out:
            return self._result(CheckStatus.FAILED, f"Output does not contain {self.contains_text!r}", start, details)
        return self._result(CheckStatus.PASSED, f"Exit code {proc.returncode}", start, details)

    def _result(self, status: CheckStatus, message: str, start: float, details: Optional[dict] = None) -> CheckResult:
        return CheckResult(
            name=self.name,
            category=self.category,
            status=status,
            message=message,
            target=self.display,
            latency_ms=(time.perf_counter() - start) * 1000.0,
            details=details or {},
        )
