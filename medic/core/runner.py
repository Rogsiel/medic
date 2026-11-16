"""Check runner and execution engine."""

import concurrent.futures
import threading
import time
import traceback
from datetime import datetime, timezone
from typing import Callable, Dict, List, Optional

from .base import BaseCheck
from .enums import CheckCategory, CheckStatus
from .models import CheckResult, MedicReport

# Extra seconds a check gets beyond its own ``timeout`` before the runner
# gives up on it. Checks enforce their own socket/subprocess timeouts; this
# is the backstop for anything that hangs regardless.
TIMEOUT_GRACE_SEC = 2.0


class MedicRunner:
    """Orchestrates execution of health checks.

    Checks are grouped by ``check.stage`` and stages run in ascending order.
    If any check in a stage fails, every check in later stages is skipped
    (checkpoint semantics). With ``stop_on_failure`` the run is sequential and
    stops at the first failing check.
    """

    def __init__(
        self,
        checks: Optional[List[BaseCheck]] = None,
        parallel: bool = True,
        max_workers: int = 8,
        stop_on_failure: bool = False,
        on_result: Optional[Callable[[CheckResult], None]] = None,
    ) -> None:
        self.checks: List[BaseCheck] = checks or []
        self.parallel = parallel
        self.max_workers = max_workers
        self.stop_on_failure = stop_on_failure
        self.on_result = on_result
        self._callback_lock = threading.Lock()

    def add_check(self, check: BaseCheck) -> "MedicRunner":
        """Add a single check to the runner."""
        self.checks.append(check)
        return self

    def add_checks(self, checks: List[BaseCheck]) -> "MedicRunner":
        """Add multiple checks to the runner."""
        self.checks.extend(checks)
        return self

    def _emit(self, results: List[CheckResult]) -> None:
        if self.on_result:
            with self._callback_lock:
                for r in results:
                    self.on_result(r)

    @staticmethod
    def _call_with_timeout(check: BaseCheck, timeout: float):
        """Run check.run() in a daemon thread; return (done, value, exc, tb)."""
        box: Dict[str, object] = {}

        def target() -> None:
            try:
                box["value"] = check.run()
            except BaseException as exc:  # noqa: BLE001 - reported as ERROR result
                box["exc"] = exc
                box["tb"] = traceback.format_exc()

        # Daemon thread: a hung check must not keep the interpreter alive.
        thread = threading.Thread(target=target, name=f"medic-{getattr(check, 'name', 'check')}", daemon=True)
        thread.start()
        thread.join(timeout)
        return (not thread.is_alive()), box

    def _execute_check_safe(self, check: BaseCheck) -> List[CheckResult]:
        """Safely execute a single check, enforcing its timeout and catching exceptions."""
        name = getattr(check, "name", type(check).__name__)
        category = getattr(check, "category", CheckCategory.SYSTEM)

        if not check.enabled:
            results = [CheckResult(name=name, category=category, status=CheckStatus.SKIPPED, message="Check is disabled")]
            self._emit(results)
            return results

        limit = float(getattr(check, "timeout", 10.0)) + TIMEOUT_GRACE_SEC
        start_time = time.perf_counter()
        done, box = self._call_with_timeout(check, limit)
        elapsed_ms = (time.perf_counter() - start_time) * 1000.0

        if not done:
            results = [
                CheckResult(
                    name=name,
                    category=category,
                    status=CheckStatus.FAILED,
                    message=f"Timed out after {limit:.1f}s",
                    latency_ms=elapsed_ms,
                    details={"timeout_sec": limit},
                    remediation="The check hung past its timeout; verify the target is reachable.",
                )
            ]
        elif "exc" in box:
            exc = box["exc"]
            assert isinstance(exc, BaseException)
            results = [
                CheckResult(
                    name=name,
                    category=category,
                    status=CheckStatus.ERROR,
                    message=f"Unhandled exception: {str(exc) or exc.__class__.__name__}",
                    latency_ms=elapsed_ms,
                    details={
                        "exception": exc.__class__.__name__,
                        "error": str(exc),
                        "traceback": box.get("tb", ""),
                    },
                )
            ]
        else:
            raw = box.get("value")
            if isinstance(raw, list):
                results = raw
            elif isinstance(raw, CheckResult):
                results = [raw]
            else:
                results = [
                    CheckResult(
                        name=name,
                        category=category,
                        status=CheckStatus.ERROR,
                        message=f"Check returned invalid result type: {type(raw).__name__}",
                        latency_ms=elapsed_ms,
                    )
                ]
            # Only a lone result can be attributed the whole check's wall time;
            # multi-result checks report their own per-result latency.
            if len(results) == 1 and results[0].latency_ms is None:
                results[0].latency_ms = elapsed_ms

        self._emit(results)
        return results

    def _skipped_for_stage(self, check: BaseCheck, failed_stage: int) -> List[CheckResult]:
        results = [
            CheckResult(
                name=getattr(check, "name", type(check).__name__),
                category=getattr(check, "category", CheckCategory.SYSTEM),
                status=CheckStatus.SKIPPED,
                message=f"Skipped: stage {failed_stage} failed",
            )
        ]
        self._emit(results)
        return results

    def _run_stage(self, checks: List[BaseCheck]) -> List[CheckResult]:
        """Run one stage's checks; returns results in registration order."""
        if not self.parallel or self.stop_on_failure or len(checks) == 1:
            out: List[CheckResult] = []
            for check in checks:
                results = self._execute_check_safe(check)
                out.extend(results)
                if self.stop_on_failure and any(r.is_failure for r in results):
                    break
            return out

        workers = max(1, min(len(checks), self.max_workers))
        indexed: List[List[CheckResult]] = [[] for _ in checks]
        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as executor:
            future_to_index = {executor.submit(self._execute_check_safe, c): i for i, c in enumerate(checks)}
            for future in concurrent.futures.as_completed(future_to_index):
                idx = future_to_index[future]
                try:
                    indexed[idx] = future.result()
                except Exception as e:  # _execute_check_safe should not raise
                    indexed[idx] = [
                        CheckResult(
                            name=getattr(checks[idx], "name", f"Check-{idx}"),
                            category=getattr(checks[idx], "category", CheckCategory.SYSTEM),
                            status=CheckStatus.ERROR,
                            message=f"Thread execution failed: {e}",
                        )
                    ]
        return [r for res in indexed for r in res]

    def run(self) -> MedicReport:
        """Run all configured checks and produce a MedicReport."""
        start_time = time.perf_counter()
        all_results: List[CheckResult] = []

        stages: Dict[int, List[BaseCheck]] = {}
        for check in self.checks:
            stages.setdefault(getattr(check, "stage", 0), []).append(check)

        failed_stage: Optional[int] = None
        for stage in sorted(stages):
            if failed_stage is not None:
                for check in stages[stage]:
                    all_results.extend(self._skipped_for_stage(check, failed_stage))
                continue
            results = self._run_stage(stages[stage])
            all_results.extend(results)
            if any(r.is_failure for r in results):
                failed_stage = stage

        return MedicReport(
            results=all_results,
            timestamp=datetime.now(timezone.utc),
            duration_ms=(time.perf_counter() - start_time) * 1000.0,
        )
