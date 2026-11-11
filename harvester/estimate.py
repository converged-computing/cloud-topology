#!/usr/bin/env python3
"""Estimate how many search queries remain, per directive, from saturation rates seen so far."""

import argparse
import json
from collections import Counter, deque

import store
from harvest import without_license_search
from websearch import build_query, children


def walk(db, plan, directive):
    """Return (pending depths, done count per depth, saturated count per depth)."""
    pending, done, saturated = Counter(), Counter(), Counter()
    queue = deque([([], 0)])
    while queue:
        clauses, depth = queue.popleft()
        row = db.execute("SELECT status, saturated FROM queries WHERE query=?",
                         (build_query(plan, directive, clauses),)).fetchone()
        if not row or row[0] not in ("done", "empty"):
            pending[depth] += 1
            continue
        done[depth] += 1
        if row[1]:
            saturated[depth] += 1
            queue.extend(children(plan, directive, clauses, depth))
    return pending, done, saturated


def branching(plan, directive, depth):
    """Number of child queries a saturated query at depth produces."""
    return len(children(plan, directive, [], depth))


def estimate(db, plan, directive):
    """Expected remaining queries: each pending query plus its expected descendants."""
    pending, done, saturated = walk(db, plan, directive)
    levels = len(plan["split"]) + 1
    rate = {d: (saturated[d] / done[d]) if done[d] else None for d in range(levels)}
    # Levels with no finished queries yet borrow the rate of the deepest level that has one.
    known = [d for d in range(levels) if rate[d] is not None]
    for d in range(levels):
        if rate[d] is None:
            rate[d] = rate[max(k for k in known if k <= d)] if known and min(known) <= d else 1.0
    expected = [1.0] * (levels + 1)
    for d in range(levels - 1, -1, -1):
        b = branching(plan, directive, d)
        expected[d] = 1 + (rate[d] * b * expected[d + 1] if b else 0)
    remaining = sum(n * expected[d] for d, n in pending.items())
    return remaining, pending, rate


def main():
    """Command-line entry point."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", default="harvest.sqlite")
    parser.add_argument("--plan", default="plan.json")
    parser.add_argument("--per-day", type=float, help="queries you run per day")
    args = parser.parse_args()
    db = store.connect(args.db)
    with open(args.plan) as fh:
        plan = without_license_search(json.load(fh))
    for directive in plan["directives"]:
        remaining, pending, rate = estimate(db, plan, directive)
        rates = " ".join(f"d{d}={r:.0%}" for d, r in rate.items())
        line = (f"{directive:7s} pending now {sum(pending.values()):6d}  "
                f"expected remaining ~{remaining:,.0f}  saturation by level: {rates}")
        if args.per_day:
            line += f"  ~{remaining / args.per_day:,.0f} days"
        print(line)


if __name__ == "__main__":
    main()
