"""Time each query of the database backend on its own, on a copy of a database, with as
many synthetic allocations on record as asked.

  python3 profile.py --db graphs/db/graph.lbug --jobspecs jobspecs --allocations 10000

For every single-phase jobspec the script runs what the service runs for one match, one
query at a time: the match query (offers with the shape, with free nodes over the
window), the merge and ranking in Python, the free check of the chosen offer's zones, the
reserve write, the lookup by request a cancel does, and the release; plus the
earliest-start query for the two best offers, which the service only runs when they are
short of nodes, timed here every time. It prints one row per kind with its median, p95
and worst, and the sum a match pays. --threads 0 lets LadybugDB use every core."""

from __future__ import annotations

import argparse
import csv
import json
import os
import random
import shutil
import statistics
import sys
import tempfile
import time
from datetime import datetime, timedelta, timezone

import yaml

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "ingest-agent"))
from ingest_agent.engine import MAX_ROWS, merge, rank  # noqa: E402
from ingest_agent.graphdb import GraphDB  # noqa: E402
from ingest_agent.models import Request, Window  # noqa: E402
from ingest_agent.prices import Prices  # noqa: E402
from ingest_agent.query import (allocations_cypher, earliest_cypher, free_cypher, match_cypher, release_cypher,  # noqa: E402
                                reserve_cypher)

NOW = datetime(2026, 11, 9, 12, tzinfo=timezone.utc)


def seed_allocations(db: GraphDB, n: int, seed: int = 1) -> None:
    """n allocations of one node each over random offers, starting within half a day of
    NOW either way and lasting one to eight hours, loaded in one COPY."""
    if n <= 0:
        return
    offers = [r["path"] for r in db.query("MATCH (o:Vertex {type: 'offer', level: 'exact'}) RETURN o.path AS path")]
    rng = random.Random(seed)
    with tempfile.NamedTemporaryFile("w", suffix=".csv", delete=False, newline="") as f:
        w = csv.writer(f)
        for i in range(n):
            start = NOW + timedelta(seconds=rng.randint(-43200, 43200))
            end = start + timedelta(seconds=rng.randint(3600, 8 * 3600))
            w.writerow([f"seed{i:08d}", rng.choice(offers), 1, start.strftime("%Y-%m-%d %H:%M:%S"), end.strftime("%Y-%m-%d %H:%M:%S"), f"seed:{i}", ""])
    db.query(f"COPY Allocation FROM '{f.name}' (HEADER=false)")
    os.unlink(f.name)


class Timer:
    def __init__(self):
        self.samples: dict[str, list[float]] = {}

    def run(self, kind: str, fn):
        t = time.perf_counter()
        out = fn()
        self.samples.setdefault(kind, []).append(1000 * (time.perf_counter() - t))
        return out

    def row(self, kind: str) -> dict:
        s = sorted(self.samples.get(kind, []))
        if not s:
            return {"kind": kind, "n": 0}
        return {"kind": kind, "n": len(s), "median": statistics.median(s), "p95": s[int(0.95 * (len(s) - 1))], "max": s[-1]}


def profile(db: GraphDB, jobspecs: str, limit: int) -> tuple[Timer, dict]:
    jobs = [l.split("\t") for l in open(os.path.join(jobspecs, "jobs.tsv")).read().splitlines()[1:]]
    ids = [j[0] for j in jobs if j[1] == "single"][:limit]
    timer, prices = Timer(), Prices()
    placed = 0
    for i, id in enumerate(ids):
        r = Request.parse(yaml.safe_load(open(os.path.join(jobspecs, "exact", f"{id}.yaml"))))
        r.validate()
        r.window = Window(start=NOW, duration=r.window.duration if r.window else 0)
        rows = timer.run("match query", lambda: db.query(match_cypher(r, MAX_ROWS, NOW)))
        cands = timer.run("merge and rank (Python)", lambda: rank(merge(rows), r, prices, "fit"))
        if not cands:
            continue
        best = cands[0]
        zones = best.zones or [best.zone]
        paths = [best.path(z) for z in zones]
        start, end = r.window.resolve(NOW)
        have = timer.run("free check", lambda: db.query(free_cypher(paths, start, end)))
        top = [c.path(z) for c in cands[:2] for z in (c.zones or [c.zone])]
        timer.run("earliest start (when short)", lambda: db.query(earliest_cypher(sorted(top), r.nodes, start, r.window.duration)))
        path = next((p for p in paths if any(h["path"] == p and int(h["free"]) >= r.nodes for h in have)), None)
        if path is None:
            continue
        placed += 1
        aid = f"prof{i:04d}"
        timer.run("reserve (write)", lambda: db.query(reserve_cypher(aid, path, r.nodes, start, end, f"flux:{i}", "")))
        timer.run("lookup by request (cancel)", lambda: db.query(allocations_cypher(request=f"flux:{i}")))
        timer.run("release (write)", lambda: db.query(release_cypher(aid)))
    stats = {"jobs": len(ids), "placed": placed}
    return timer, stats


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    p.add_argument("--db", required=True)
    p.add_argument("--jobspecs", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "jobspecs"))
    p.add_argument("--allocations", type=int, default=0, help="synthetic allocations on record before timing")
    p.add_argument("--threads", type=int, default=1)
    p.add_argument("--limit", type=int, default=100)
    p.add_argument("--json", help="write the rows here too")
    args = p.parse_args()
    tmp = tempfile.mkdtemp(prefix="profile-")
    try:
        path = os.path.join(tmp, "graph.lbug")
        shutil.copytree(args.db, path) if os.path.isdir(args.db) else shutil.copy(args.db, path)
        for side in (".wal", ".shadow"):
            if os.path.exists(args.db + side):
                shutil.copy(args.db + side, path + side)
        db = GraphDB(path, driver="ladybug", threads=args.threads)
        t = time.perf_counter()
        seed_allocations(db, args.allocations)
        seeded = time.perf_counter() - t
        size = {k: db.query(q)[0]["n"] for k, q in (("offers", "MATCH (o:Vertex {type: 'offer', level: 'exact'}) RETURN count(o) AS n"),
                                                     ("vertices", "MATCH (v:Vertex) RETURN count(v) AS n"),
                                                     ("allocations", "MATCH (a:Allocation) RETURN count(a) AS n"))}
        timer, stats = profile(db, args.jobspecs, args.limit)
        db.close()
    finally:
        shutil.rmtree(tmp, ignore_errors=True)
    kinds = ["match query", "merge and rank (Python)", "free check", "reserve (write)", "lookup by request (cancel)", "release (write)", "earliest start (when short)"]
    rows = [timer.row(k) for k in kinds]
    per_match = sum(r["median"] for r in rows[:4] if r.get("n"))
    print(f"{args.db}: {size['offers']:,} offers, {size['vertices']:,} vertices, {size['allocations']:,} allocations"
          + (f" (seeded in {seeded:.1f} s)" if args.allocations else "") + f"; {stats['placed']} of {stats['jobs']} jobs placed; {args.threads or 'all'} thread(s)")
    print("| query | n | median | p95 | max |\n|---|---|---|---|---|")
    for r in rows:
        if r.get("n"):
            print(f"| {r['kind']} | {r['n']} | {r['median']:.1f} ms | {r['p95']:.1f} ms | {r['max']:.1f} ms |")
    print(f"\nA match that places pays the first four: {per_match:.1f} ms at the median.")
    if args.json:
        json.dump({"db": args.db, "size": size, "threads": args.threads, "rows": rows, "per_match_median_ms": per_match, **stats}, open(args.json, "w"), indent=1)
    return 0


if __name__ == "__main__":
    sys.exit(main())
