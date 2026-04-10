#!/usr/bin/env python3
"""Fuzzy dedup for hypomnema mistakes. Requires: rapidfuzz>=3.6"""

import os
import re
import sys
from pathlib import Path

try:
    from rapidfuzz.fuzz import token_set_ratio
except ImportError:
    sys.exit(0)

MERGE_THRESHOLD = 80
CANDIDATE_THRESHOLD = 50


def extract_root_cause(filepath: Path) -> str:
    text = filepath.read_text(encoding="utf-8", errors="replace")
    m = re.search(r'^root-cause:\s*["\']?(.+?)["\']?\s*$', text, re.MULTILINE)
    return m.group(1).strip() if m else ""


def increment_recurrence(filepath: Path) -> None:
    text = filepath.read_text(encoding="utf-8", errors="replace")
    def inc(match):
        return f"recurrence: {int(match.group(1)) + 1}"
    new_text = re.sub(r"^recurrence:\s*(\d+)", inc, text, count=1, flags=re.MULTILINE)
    filepath.write_text(new_text, encoding="utf-8")


def main():
    if len(sys.argv) < 2:
        sys.exit(0)

    new_file = Path(sys.argv[1])
    if not new_file.exists():
        sys.exit(0)

    memory_dir = Path(os.environ.get("CLAUDE_MEMORY_DIR", Path.home() / ".claude" / "memory"))
    mistakes_dir = memory_dir / "mistakes"
    wal_file = memory_dir / ".wal"
    session_id = os.environ.get("HYPOMNEMA_SESSION_ID", "unknown")
    today = __import__("datetime").date.today().isoformat()

    new_rc = extract_root_cause(new_file)
    if not new_rc:
        sys.exit(0)

    best_score = 0.0
    best_file = None

    for f in mistakes_dir.glob("*.md"):
        if f == new_file:
            continue
        existing_rc = extract_root_cause(f)
        if not existing_rc:
            continue
        score = token_set_ratio(new_rc, existing_rc)
        if score > best_score:
            best_score = score
            best_file = f

    new_name = new_file.stem
    if best_file and best_score >= MERGE_THRESHOLD:
        existing_name = best_file.stem
        increment_recurrence(best_file)
        new_file.unlink()
        with open(wal_file, "a") as w:
            w.write(f"{today}|dedup-merged|{new_name}>{existing_name}|{session_id}\n")
        print(f"Merged: {new_name} -> {existing_name} (similarity {best_score:.0f}%)")
        sys.exit(1)

    if best_file and best_score >= CANDIDATE_THRESHOLD:
        existing_name = best_file.stem
        with open(wal_file, "a") as w:
            w.write(f"{today}|dedup-candidate|{new_name}~{existing_name}|{session_id}\n")
        print(f"Possible duplicate: {new_name} ~ {existing_name} ({best_score:.0f}%)")

    sys.exit(0)


if __name__ == "__main__":
    main()
