"""The resource service's decisions, with the database faked."""

import errno
import json

import pytest

from fluxion_graphdb import Resource, rv1
from ingest_agent.models import Candidate, Request, parse_time
from tests.conftest import ROWS, FakeDB

NOW = 1_800_000_000  # a fixed epoch second
P5 = "/site0/aws/us-east-1/us-east-1a/p5.48xlarge.on-demand"


def jobspec(nodes=2, cores=48, gpus=8, duration=3600):
    js = Request.from_dict({"nodes": nodes, "node": {"cores": cores, "gpus": gpus}, "when": {"duration": duration}}).to_jobspec()
    return js


def test_allocate_now_reserve_later_and_cancel():
    db = FakeDB(free=[{"path": P5, "nodes": 2, "free": 2}])
    res = Resource(db)
    out = res.match("allocate", 1, jobspec(), now=NOW)
    assert out["status"] == "ALLOCATED" and out["at"] == NOW and out["jobid"] == 1
    R = json.loads(out["R"])
    assert R["scheduling"]["offer"] == P5 and R["scheduling"]["nodes"] == 2 and R["execution"]["expiration"] == NOW + 3600
    assert R["execution"]["nodelist"] == ["p5-48xlarge-us-east-1a-[0-1]"] and R["execution"]["R_lite"][0]["children"] == {"core": "0-95", "gpu": "0-7"}
    one = rv1(Candidate.from_row({**ROWS[0], "cores": 1, "gpus": 1}), P5, "us-east-1a", 1, NOW, 0)
    assert one["execution"]["R_lite"] == [{"rank": "0", "children": {"core": "0", "gpu": "0"}}] and one["execution"]["nodelist"] == ["p5-48xlarge-us-east-1a-0"] and one["execution"]["expiration"] == 0
    assert db.queries[-1].startswith("CREATE (:Allocation") and "request: 'flux:1'" in db.queries[-1]
    assert res.info(1)["status"] == "ALLOCATED" and res.stats()["njobs"] == 1
    with pytest.raises(OSError) as e:
        res.match("allocate", 1, jobspec(), now=NOW)
    assert e.value.errno == errno.EEXIST
    # Nothing free now: allocate fails EBUSY, allocate_orelse_reserve reserves at the earliest start.
    booked = [{**r, "free": 0} for r in ROWS]
    db = FakeDB(rows=booked, earliest=[{"path": P5, "available_at": "2027-01-15 12:00:00"}], free=[{"path": P5, "nodes": 2, "free": 2}])
    res = Resource(db)
    with pytest.raises(OSError) as e:
        res.match("allocate", 2, jobspec(), now=NOW)
    assert e.value.errno == errno.EBUSY
    out = res.match("allocate_orelse_reserve", 2, jobspec(), now=NOW)

    assert out["at"] == int(parse_time("2027-01-15T12:00Z").timestamp()) and res.info(2)["status"] == "RESERVED"
    db.allocations = [{"id": "a1", "offer": P5, "nodes": "2", "from_time": "2027-01-15 12:00:00", "to_time": "2027-01-15 13:00:00", "request": "flux:2", "note": ""}]
    res.cancel(2)
    assert any("DELETE a" in q for q in db.queries) and 2 not in res.jobs
    with pytest.raises(OSError) as e:
        res.cancel(2)
    assert e.value.errno == errno.ENOENT


def test_unsatisfiable_bad_jobspec_and_satisfiability():
    res = Resource(FakeDB(rows=[]))
    with pytest.raises(OSError) as e:
        res.match("allocate_orelse_reserve", 3, jobspec(), now=NOW)
    assert e.value.errno == errno.ENODEV
    with pytest.raises(OSError) as e:
        res.match("allocate", 4, {"version": 1}, now=NOW)
    assert e.value.errno == errno.EINVAL
    with pytest.raises(OSError) as e:
        res.match("grow", 4, jobspec(), now=NOW)
    assert e.value.errno == errno.EINVAL
    res = Resource(FakeDB())
    out = res.match("satisfiability", 5, jobspec(), now=NOW)
    assert out["status"] == "MATCHED" and 5 not in res.jobs and not any(q.startswith("CREATE") for q in res.db.queries)


def test_reload_and_update():
    held = [{"id": "a1", "offer": P5, "nodes": "2", "from_time": "2020-01-01 00:00:00", "to_time": "9999-01-01 00:00:00", "request": "flux:9", "note": "{}"},
            {"id": "a2", "offer": P5, "nodes": "1", "from_time": "2020-01-01 00:00:00", "to_time": "2020-01-02 00:00:00", "request": "someone-else", "note": ""}]
    res = Resource(FakeDB(allocations=held, free=[{"path": P5, "nodes": 2, "free": 2}]))
    assert list(res.jobs) == [9] and res.jobs[9]["status"] == "ALLOCATED"
    R = rv1(Candidate.from_row(ROWS[0]), P5, "us-east-1a", 2, NOW, 3600)
    res.db.allocations = []
    out = res.update(11, R, now=NOW)
    assert out["status"] == "ALLOCATED" and 11 in res.jobs and "request: 'flux:11'" in res.db.queries[-1]
    with pytest.raises(OSError) as e:
        res.update(12, {"version": 1}, now=NOW)
    assert e.value.errno == errno.EINVAL
