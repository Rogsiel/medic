"""Shell completion script generation and installation."""

import os
from pathlib import Path
from typing import Optional, Tuple

import argcomplete

SHELLS = ("bash", "zsh", "fish")


def detect_shell() -> Optional[str]:
    """Best-effort login shell detection from $SHELL."""
    name = os.path.basename(os.environ.get("SHELL", ""))
    return name if name in SHELLS else None


def script_for(shell: str) -> str:
    return argcomplete.shellcode(["medic"], shell=shell)


def target_path(shell: str) -> Path:
    """Directory/file the shell autoloads completions from, with no rc edits needed."""
    home = Path.home()
    if shell == "zsh":
        # oh-my-zsh puts custom/completions on fpath; otherwise use the XDG-ish dir.
        omz = Path(os.environ.get("ZSH", home / ".oh-my-zsh"))
        if omz.is_dir():
            return omz / "custom" / "completions" / "_medic"
        return home / ".local" / "share" / "zsh" / "site-functions" / "_medic"
    if shell == "fish":
        return home / ".config" / "fish" / "completions" / "medic.fish"
    # bash-completion lazily loads this directory on first <TAB> for `medic`.
    data = Path(os.environ.get("XDG_DATA_HOME", home / ".local" / "share"))
    return data / "bash-completion" / "completions" / "medic"


def install(shell: str) -> Tuple[Path, str]:
    """Write the completion file; returns (path, note about any remaining step)."""
    path = target_path(shell)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(script_for(shell), encoding="utf-8")
    note = ""
    if shell == "zsh" and "oh-my-zsh" not in str(path):
        note = f"Ensure {path.parent} is on your fpath (before compinit) in ~/.zshrc."
    return path, note
