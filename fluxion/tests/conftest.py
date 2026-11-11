import os
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(__file__)))

P5 = dict(provider="aws", region="us-east-1", machine_type="p5.48xlarge", cores=96, vcpus=192, gpus=8, accelerator="nvidia-h100",
          memory_gb=2048, egress_gbps=3200, nodes=2, group_nodes=2, topology="hwloc")
C6A = dict(provider="aws", region="us-east-1", machine_type="c6a.16xlarge", cores=32, vcpus=64, gpus=0, accelerator="",
           memory_gb=128, egress_gbps=25, nodes=64, group_nodes=64, topology="derived:c6a.48xlarge")

ROWS = [
    {**P5, "zone": "us-east-1a", "model": "on-demand"},
    {**P5, "zone": "us-east-1b", "model": "on-demand"},
    {**C6A, "zone": "us-east-1a", "model": "on-demand"},
]


class FakeDB:
    """Answers the matcher's queries from fixed rows and records the Cypher it was given."""

    def __init__(self, rows=None, earliest=None, free=None, allocations=None):
        self.rows = ROWS if rows is None else rows
        self.earliest = earliest or []  # rows for the earliest-start query: path, available_at
        self.free = free or []  # rows for the free query: path, nodes, free
        self.allocations = allocations or []
        self.queries = []

    def query(self, cypher):
        self.queries.append(cypher)
        if "RETURN DISTINCT o.provider" in cypher:  # the match query, with or without a window
            return [dict(r) for r in self.rows]
        if "RETURN count(v) AS n" in cypher or "RETURN count(c) AS n" in cypher:
            return [{"n": 5}]
        if "AS available_at" in cypher:
            return [dict(r) for r in self.earliest]
        if cypher.rstrip().endswith("RETURN o.path AS path, nodes, free;"):
            return [dict(r) for r in self.free]
        if cypher.startswith("CREATE (:Allocation"):
            return []
        if "MATCH (a:Allocation" in cypher:
            return [] if "DELETE a" in cypher else [dict(r) for r in self.allocations]
        return [dict(r) for r in self.rows]
