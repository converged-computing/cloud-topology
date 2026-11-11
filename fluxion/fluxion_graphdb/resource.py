"""The decisions behind sched-fluxion-resource, with the database doing the work and no Flux
in sight, so they can be tested. qmanager's questions arrive as Resource.match (allocate,
allocate_orelse_reserve, allocate_with_satisfiability or satisfiability), cancel, update,
info and stats; failures are OSError with the errno Fluxion uses: EBUSY for nothing free
now, ENODEV for nothing that could ever fit.

R names the offer and the node count, not hosts: the decision is where and when, and the
site holding that offer's exact graph places and runs it. Allocations are written to the
database as request flux:<jobid>, so a restart picks them up again.
"""

from __future__ import annotations

import errno
import json
import time
from datetime import datetime, timezone

from ingest_agent.engine import NEVER, allocations, free, match, release, reserve
from ingest_agent.graphdb import Database
from ingest_agent.models import Candidate, Request, Window, parse_time
from ingest_agent.prices import Prices

SERVICE = "sched-fluxion-resource"
# What a job's resources say under R.scheduling, the key the sites read.
SCHEDULING = ("offer", "provider", "region", "zone", "machine_type", "model", "nodes", "price")


def epoch(t: datetime) -> int:
    return int(t.timestamp())


def at_time(seconds: float) -> datetime:
    return datetime.fromtimestamp(int(seconds), tz=timezone.utc)


def idset(n: int) -> str:
    """0 to n-1 as a Flux idset; a single id is written alone, since 0-0 is invalid."""
    return "0" if n <= 1 else f"0-{n - 1}"


def rv1(c: Candidate, path: str, zone: str, nodes: int, at: int, duration: int) -> dict:
    """An RV1 resource set for nodes of the offer: ranks and cores as Flux expects, a node
    list named after the offer, and the offer itself under scheduling."""
    host = f"{c.machine_type}-{zone}".replace(".", "-")
    nodelist = f"{host}-[{idset(nodes)}]" if nodes > 1 else f"{host}-0"
    children = {"core": idset(max(c.cores, 1))}
    if c.gpus:
        children["gpu"] = idset(c.gpus)
    return {
        "version": 1,
        "execution": {"R_lite": [{"rank": idset(nodes), "children": children}],
                      "starttime": at, "expiration": at + duration if duration else 0, "nodelist": [nodelist]},
        "scheduling": {"offer": path, "provider": c.provider, "region": c.region, "zone": zone, "machine_type": c.machine_type,
                       "model": c.model, "nodes": nodes, "price": c.price},
    }


class Resource:
    """The service's state and decisions, independent of Flux so they can be tested.
    Methods raise OSError with Fluxion's errno for the failures qmanager understands."""

    def __init__(self, db: Database, prices: Prices | None = None, rank: str = "fit"):
        self.db, self.prices, self.by = db, prices or Prices(), rank
        self.jobs: dict[int, dict] = {}
        self.matches: list[float] = []
        self.loaded = time.time()
        self.reload()

    def reload(self) -> None:
        """Pick up the jobs an earlier run of the service wrote to the database."""
        now = time.time()
        for a in allocations(self.db):
            if not a["request"].startswith("flux:"):
                continue
            jobid = int(a["request"][5:])
            at = epoch(parse_time(a["from_time"]))
            self.jobs[jobid] = {"status": "ALLOCATED" if at <= now else "RESERVED", "at": at, "overhead": 0.0, "R": a["note"] or ""}

    def match(self, cmd: str, jobid: int, jobspec: dict | str, now: float | None = None) -> dict:
        """Fluxion's match: allocate, allocate_orelse_reserve, allocate_with_satisfiability
        or satisfiability. Returns the response payload."""
        t0 = time.monotonic()
        if cmd not in ("allocate", "allocate_orelse_reserve", "allocate_with_satisfiability", "satisfiability"):
            raise OSError(errno.EINVAL, f"unknown match command {cmd}")
        if jobid in self.jobs:
            raise OSError(errno.EEXIST, f"existent job {jobid}")
        try:
            r = Request.parse(json.loads(jobspec) if isinstance(jobspec, str) else jobspec)
            r.validate()
        except (ValueError, TypeError, KeyError, AttributeError) as e:
            raise OSError(errno.EINVAL, f"bad jobspec: {e}") from None
        start = at_time(now if now is not None else time.time())
        # Like Fluxion, the match starts from now; the jobspec's duration bounds it.
        r.window = Window(start=start, duration=r.window.duration if r.window else 0)
        cands = match(self.db, r, self.prices, self.by, now=start)
        if not cands:
            raise OSError(errno.ENODEV, "no offer in any cloud can hold the request")  # unsatisfiable
        best = cands[0]
        if cmd == "satisfiability":
            return self._response(jobid, "MATCHED", rv1(best, best.path(best.zones[0] if best.zones else best.zone), best.zones[0] if best.zones else best.zone, r.nodes, epoch(start), r.window.duration), epoch(start), t0)
        if best.fits_now:
            at = start
        elif cmd == "allocate_orelse_reserve" and best.available_at != NEVER:
            at = parse_time(best.available_at)
        else:
            raise OSError(errno.EBUSY, "no offer has the nodes free now")
        end = None if not r.window.duration else at_time(epoch(at) + r.window.duration)
        path, zone = self._place(best, r.nodes, at, end)
        R = rv1(best, path, zone, r.nodes, epoch(at), r.window.duration)
        reserve(self.db, path, r.nodes, at, end, request=f"flux:{jobid}", note=json.dumps(R["scheduling"]))
        status = "ALLOCATED" if at == start else "RESERVED"
        return self._response(jobid, status, R, epoch(at), t0)

    def _place(self, c: Candidate, nodes: int, at: datetime, end: datetime | None) -> tuple[str, str]:
        """The candidate's zone with the nodes free over the span: its offer path and zone."""
        zones = c.zones or [c.zone]
        paths = {c.path(z): z for z in zones}
        have = free(self.db, list(paths), at, end)
        for path, zone in paths.items():
            if have.get(path, (0, 0))[1] >= nodes:
                return path, zone
        raise OSError(errno.EBUSY, f"{c.machine_type} lost its free nodes between match and placement")

    def _response(self, jobid: int, status: str, R: dict, at: int, t0: float) -> dict:
        overhead = time.monotonic() - t0
        self.matches.append(overhead)
        if status != "MATCHED":
            self.jobs[jobid] = {"status": status, "at": at, "overhead": overhead, "R": json.dumps(R)}
        return {"jobid": jobid, "status": status, "overhead": overhead, "R": json.dumps(R), "at": at}

    def cancel(self, jobid: int) -> None:
        if jobid not in self.jobs:
            raise OSError(errno.ENOENT, f"no job {jobid}")
        for a in allocations(self.db, request=f"flux:{jobid}"):
            release(self.db, a["id"])
        del self.jobs[jobid]

    def update(self, jobid: int, R: dict | str, now: float | None = None) -> dict:
        """Take an allocation as given (qmanager replays running jobs' R on restart)."""
        t0 = time.monotonic()
        R = json.loads(R) if isinstance(R, str) else R
        s = R.get("scheduling") or {}
        try:
            path, nodes = s["offer"], int(s["nodes"])
        except (KeyError, TypeError, ValueError):
            raise OSError(errno.EINVAL, "R without scheduling.offer and nodes") from None
        ex = R.get("execution") or {}
        at = at_time(ex.get("starttime") or (now if now is not None else time.time()))
        end = at_time(ex["expiration"]) if ex.get("expiration") else None
        if jobid not in self.jobs:
            reserve(self.db, path, nodes, at, end, request=f"flux:{jobid}", note=json.dumps(s))
        status = "ALLOCATED" if epoch(at) <= (now if now is not None else time.time()) else "RESERVED"
        return self._response(jobid, status, R, epoch(at), t0)

    def info(self, jobid: int) -> dict:
        if jobid not in self.jobs:
            raise OSError(errno.ENOENT, f"no job {jobid}")
        j = self.jobs[jobid]
        return {"jobid": jobid, "status": j["status"], "at": j["at"], "overhead": j["overhead"]}

    def stats(self) -> dict:
        v = self.db.query("MATCH (v:Vertex) RETURN count(v) AS n;")
        e = self.db.query("MATCH ()-[c:Contains]->() RETURN count(c) AS n;")
        m = self.matches
        return {"V": int(v[0]["n"]) if v else 0, "E": int(e[0]["n"]) if e else 0, "load-time": 0.0, "njobs": len(self.jobs),
                "min-match": min(m) if m else 0.0, "max-match": max(m) if m else 0.0, "avg-match": sum(m) / len(m) if m else 0.0}
