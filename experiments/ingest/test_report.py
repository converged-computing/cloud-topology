import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import report  # noqa: E402


def test_report_judges_runs(tmp_path):
    d = tmp_path / "claude" / "claude-opus-5-5"
    d.mkdir(parents=True)
    engine = {"candidates": [{"provider": "aws", "machine_type": "p5.48xlarge", "model": "on-demand"}]}
    (d / "gpu-training.engine.json").write_text(json.dumps(engine))
    summary = {"records": [{"provider": "aws", "machine_type": "p5.48xlarge", "model": "on-demand", "reason": "only fit"}],
               "matches_used": 1, "proposals": [{}], "usage": {"turns": 5, "cost_usd": 0.65, "wall_seconds": 27, "input_tokens": 11149}}
    (d / "gpu-training.agent.out").write_text("ResultMessage(...)\n\n" + json.dumps(summary, indent=2) + "\n")
    (d / "impossible-gpu.agent.out").write_text("\n" + json.dumps({"records": [], "matches_used": 2, "proposals": [], "usage": {"turns": 4, "cost_usd": 0.2}}, indent=2) + "\n")
    (d / "broken.agent.out").write_text("the run died\n")
    (d / "denied.agent.out").write_text("")
    (d / "denied.agent.err").write_text("Traceback...\nbotocore.errorfactory.AccessDeniedException: An error occurred (AccessDeniedException) when calling ConverseStream\nrc=1 seconds=0\n")
    rows = report.collect(tmp_path, [{"name": "gpu-training", "expect": "fits"}, {"name": "impossible-gpu", "expect": "nothing"}, {"name": "broken", "expect": "fits"}, {"name": "denied", "expect": "fits"}])
    by = {r["request"]: r for r in rows}
    assert by["gpu-training"]["verdict"] == "agrees with engine" and by["gpu-training"]["cost_usd"] == 0.65 and by["gpu-training"]["turns"] == 5
    assert by["impossible-gpu"]["verdict"] == "right: declined"
    assert by["broken"]["verdict"] == "no run"
    assert by["denied"]["verdict"] == "backend error" and by["denied"]["reason"].startswith("AccessDeniedException")
    md = report.markdown(rows)
    assert "## claude / claude-opus-5-5" in md and "2 runs (2 did not finish), 2 agree" in md and "only fit" in md
    assert report.markdown([]) == "No runs yet.\n"


def test_closest_offer_counts_as_declined_and_cause_is_checked(tmp_path):
    d = tmp_path / "aws" / "m"
    d.mkdir(parents=True)
    why = {"blocking": [{"constraint": "nodes", "limit": "at most 2 nodes in one placement group"}]}
    (d / "impossible-gpu.engine.json").write_text(json.dumps({"candidates": [], "why_none": why}))
    closest = {"provider": "aws", "machine_type": "p5.48xlarge", "model": "on-demand", "fits": False,
               "reason": "the quota allows 2 nodes in a placement group"}
    widened = {"provider": "aws", "machine_type": "p4d.24xlarge", "model": "on-demand", "fits": True, "reason": "no 400 Gbps card"}
    for tag, rec in (("impossible-gpu.1", closest), ("impossible-gpu.2", widened)):
        (d / f"{tag}.agent.out").write_text("\n" + json.dumps({"records": [rec], "usage": {}}, indent=2) + "\n")
    by = {r["request"]: r for r in report.collect(tmp_path, [{"name": "impossible-gpu", "expect": "nothing"}])}
    assert by["impossible-gpu.1"]["verdict"] == "right: declined" and by["impossible-gpu.1"]["cause"] == "quota named"
    assert by["impossible-gpu.1"]["chosen"].endswith("(closest, not a fit)")
    assert by["impossible-gpu.2"]["verdict"] == "widened" and by["impossible-gpu.2"]["cause"] == "quota not named"
