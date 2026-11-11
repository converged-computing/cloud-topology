"""fluxion-graphdb: serve Fluxion's resource protocol from the graph database. Run it under
flux python, inside the Flux instance whose qmanager should use it."""

from __future__ import annotations

import argparse
import sys

from ingest_agent.engine import RANKS
from ingest_agent.graphdb import DRIVERS, GraphDB
from ingest_agent.prices import Prices


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(prog="fluxion-graphdb",
                                description="Stand in for sched-fluxion-resource so Fluxion's qmanager schedules over the graph database.")
    p.add_argument("--db", default="graphdb/graph.lbug", help="LadybugDB database from cloud-topology's make graphdb")
    p.add_argument("--driver", default="auto", choices=DRIVERS, help="ladybug: the Python package (default when installed); shell: the lbug shell")
    p.add_argument("--shell", default="lbug", help="LadybugDB shell, for --driver shell")
    p.add_argument("--prices", action="append", default=[], help="price file as provider=path; repeatable")
    p.add_argument("--rank", default="fit", choices=RANKS, help="how candidates are ordered before the first free one is taken")
    args = p.parse_args(argv)
    try:
        import flux  # noqa: F401
    except ImportError:
        log("fluxion-graphdb needs the Flux Python bindings: run it as flux python -m fluxion_graphdb")
        return 2
    from .resource import Resource
    from .service import serve

    prices = Prices()
    for spec in args.prices:
        prices.load(spec)
    db = GraphDB(args.db, shell=args.shell, driver=args.driver)
    try:
        serve(Resource(db, prices, args.rank), log=log)
    finally:
        db.close()
    return 0
