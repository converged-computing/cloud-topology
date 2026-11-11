"""Compare two backends' runs of the same phase.

    python3 compare.py runs/fluxion-single.json runs/graphdb-single.json

Prints, in Markdown, what each backend answered (allocated, reserved, busy, unsatisfiable),
how often the two agreed on the answer and on the machine type, and their latency and
memory. --json writes the same as a file for the site."""

from __future__ import annotations

import argparse
import json
import statistics


def machine_type(offer: str) -> str:
    return offer.rsplit("/", 1)[-1].rsplit(".", 1)[0] if offer else ""


def summarize(run: dict) -> dict:
    jobs = run["jobs"]
    lat = sorted(j["latency"] for j in jobs)
    counts = {}
    for j in jobs:
        counts[j.get("status", "?")] = counts.get(j.get("status", "?"), 0) + 1
    q = lambda p: lat[min(len(lat) - 1, int(p * len(lat)))] if lat else 0.0  # noqa: E731
    return {"backend": run["backend"], "jobs": len(jobs), "counts": counts, "wall": run["wall"], "rss_mb": run["rss_mb"],
            "latency_ms": {"median": statistics.median(lat) * 1000 if lat else 0, "p95": q(0.95) * 1000, "max": (lat[-1] if lat else 0) * 1000}}


def agreement(a: dict, b: dict) -> dict:
    by = {j["id"]: j for j in b["jobs"]}
    same_status = same_type = both_placed = 0
    diffs = []
    for ja in a["jobs"]:
        jb = by.get(ja["id"])
        if not jb:
            continue
        placed = lambda j: j.get("status") in ("ALLOCATED", "RESERVED")  # noqa: E731
        if (placed(ja)) == (placed(jb)):
            same_status += 1
        if placed(ja) and placed(jb):
            both_placed += 1
            if machine_type(ja.get("offer", "")) == machine_type(jb.get("offer", "")):
                same_type += 1
            else:
                diffs.append((ja["id"], ja["description"], machine_type(ja["offer"]), machine_type(jb["offer"])))
    return {"compared": len(a["jobs"]), "same_outcome": same_status, "both_placed": both_placed, "same_machine_type": same_type, "differences": diffs}


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("a")
    p.add_argument("b")
    p.add_argument("--json")
    args = p.parse_args()
    a, b = json.load(open(args.a)), json.load(open(args.b))
    sa, sb = summarize(a), summarize(b)
    ag = agreement(a, b)
    out = {"phase": a["phase"], "cmd": a["cmd"], "a": sa, "b": sb, "agreement": ag}
    if args.json:
        with open(args.json, "w") as f:
            json.dump(out, f, indent=1)
    print(f"## {a['phase']} phase, {a['cmd']}\n")
    print("| | " + " | ".join(s["backend"] for s in (sa, sb)) + " |")
    print("|---|---|---|")
    keys = sorted(set(sa["counts"]) | set(sb["counts"]))
    for k in keys:
        print(f"| {k} | {sa['counts'].get(k, 0)} | {sb['counts'].get(k, 0)} |")
    for label, f in (("median latency", lambda s: f"{s['latency_ms']['median']:.1f} ms"), ("p95 latency", lambda s: f"{s['latency_ms']['p95']:.1f} ms"),
                     ("max latency", lambda s: f"{s['latency_ms']['max']:.1f} ms"), ("wall", lambda s: f"{s['wall']:.1f} s"), ("memory", lambda s: f"{s['rss_mb']:.0f} MB")):
        print(f"| {label} | {f(sa)} | {f(sb)} |")
    print(f"\nSame outcome (placed or not) on {ag['same_outcome']} of {ag['compared']} jobs; "
          f"same machine type on {ag['same_machine_type']} of the {ag['both_placed']} both placed.\n")
    if ag["differences"]:
        print(f"| job | request | {sa['backend']} | {sb['backend']} |\n|---|---|---|---|")
        for id, desc, ta, tb in ag["differences"][:40]:
            print(f"| {id} | {desc} | {ta} | {tb} |")
        if len(ag["differences"]) > 40:
            print(f"\n{len(ag['differences']) - 40} more.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
