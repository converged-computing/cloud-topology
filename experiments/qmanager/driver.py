"""Send the workload to whichever sched-fluxion-resource is loaded and record what it said.

    flux python driver.py --backend fluxion --phase single --out runs/fluxion-single.json

Speaks the protocol qmanager speaks: match with a command (allocate_orelse_reserve by
default), cancel, stats-get. The single phase matches and cancels each job in turn, so
every job sees an empty graph; the stream phase matches every job and keeps the
allocations, so later jobs see what earlier ones took. Runs under flux python inside a
Flux instance (see run.sh)."""

from __future__ import annotations

import argparse
import csv
import errno as errnos
import json
import os
import sys
import time

import yaml

import flux

SERVICE = "sched-fluxion-resource"


def offer_of(R: dict) -> str:
    """The offer an R names: ours under scheduling.offer, Fluxion's in the JGF it returns
    (any vertex path, cut at the offer). Fluxion's rv1 writer needs ranks our graphs do not
    have, so run.sh asks it for jgf."""
    s = R.get("scheduling") or {}
    if "offer" in s:
        return s["offer"]
    graph = s.get("graph") or R.get("graph") or {}  # Fluxion's rv1 puts the JGF under scheduling; its jgf format is the JGF
    for node in graph.get("nodes") or []:
        path = ((node.get("metadata") or {}).get("paths") or {}).get("containment", "")
        parts = path.split("/")
        if len(parts) >= 6:
            return "/".join(parts[:6])
    return ""


def rss_mb(pid: int) -> float:
    try:
        with open(f"/proc/{pid}/status") as f:
            for line in f:
                if line.startswith("VmRSS:"):
                    return int(line.split()[1]) / 1024
    except OSError:
        pass
    return 0.0


def wait_for_service(h, tries=100) -> None:
    """Until the service answers params (both backends have it)."""
    for _ in range(tries):
        try:
            h.rpc(SERVICE + ".params").get()
            return
        except OSError as e:
            if e.errno not in (errnos.ENOSYS, errnos.ECONNREFUSED, errnos.EHOSTUNREACH, errnos.ENODATA):
                raise
            time.sleep(0.2)
    raise SystemExit(f"{SERVICE} did not answer")


def stats(h) -> dict | None:
    """stats-get where it works; Fluxion's fails on a graph whose vertices carry no rank."""
    try:
        return h.rpc(SERVICE + ".stats-get").get()
    except OSError:
        return None


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--backend", required=True, help="a label: fluxion or graphdb")
    p.add_argument("--jobspecs", default="jobspecs", help="directory with jobs.tsv and exact/<id>.yaml")
    p.add_argument("--phase", default="single", choices=["single", "stream"])
    p.add_argument("--cmd", default="allocate_orelse_reserve")
    p.add_argument("--limit", type=int, default=0, help="only the first N jobs")
    p.add_argument("--rss-pid", type=int, default=0, help="process whose memory to record")
    p.add_argument("--out", required=True)
    args = p.parse_args()

    with open(os.path.join(args.jobspecs, "jobs.tsv")) as f:
        jobs = [r for r in csv.DictReader(f, delimiter="\t") if r["phase"] == args.phase and "exact" in r["levels"]]
    if args.limit:
        jobs = jobs[: args.limit]
    h = flux.Flux()
    wait_for_service(h)
    stats_before = stats(h)
    results, started = [], time.time()
    jobid = 1
    for job in jobs:
        with open(os.path.join(args.jobspecs, "exact", job["id"] + ".yaml")) as f:
            spec = yaml.safe_load(f)
        rec = {"id": job["id"], "description": job["description"], "jobid": jobid, "nodes": int(job["nodes"])}
        now = time.time()
        try:
            out = h.rpc(SERVICE + ".match", {"cmd": args.cmd, "jobid": jobid, "jobspec": json.dumps(spec)}).get()
            rec.update(status=out["status"], at_offset=int(out["at"]) - int(now), overhead=out["overhead"], offer=offer_of(json.loads(out["R"])))
        except OSError as e:
            rec.update(status=errnos.errorcode.get(e.errno, str(e.errno)), error=str(e))
        rec["latency"] = time.time() - now
        if rec.get("status") in ("ALLOCATED", "RESERVED") and args.phase == "single":
            t = time.time()
            h.rpc(SERVICE + ".cancel", {"jobid": jobid}).get()
            rec["cancel_latency"] = time.time() - t
        results.append(rec)
        jobid += 1
        print(f"{rec['id']:<12} {rec.get('status', ''):<10} {rec.get('offer', '').rsplit('/', 1)[-1]:<28} {rec['latency'] * 1000:7.1f} ms", file=sys.stderr)
    wall = time.time() - started
    rss = rss_mb(args.rss_pid) if args.rss_pid else 0.0
    stats_after = stats(h)
    if args.phase == "stream":
        for rec in results:
            if rec.get("status") in ("ALLOCATED", "RESERVED"):
                h.rpc(SERVICE + ".cancel", {"jobid": rec["jobid"]}).get()
    report = {"backend": args.backend, "phase": args.phase, "cmd": args.cmd, "jobs": results, "wall": wall, "rss_mb": rss,
              "stats_before": stats_before, "stats_after": stats_after}
    os.makedirs(os.path.dirname(args.out) or ".", exist_ok=True)
    with open(args.out, "w") as f:
        json.dump(report, f, indent=1)
    print(f"{args.backend} {args.phase}: {len(results)} jobs in {wall:.1f}s, rss {rss:.0f} MB -> {args.out}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
