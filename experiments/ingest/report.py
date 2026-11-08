"""The ingest experiment's report: for every backend, model and request, what the agent
recommended against what the engine ranked first, whether it kept to the floor, and what
the run cost. Reads the results directory run.sh writes; prints Markdown.

Usage: python3 report.py [results] [--json rows.json] [--site ingest.json [--earlier label=dir ...]]

--site writes what cloud-topology's site shows: the question and findings (findings.yaml),
the requests, and each batch's rows and per-arm summary; --earlier adds an archived batch
under a label for comparison.
"""

from __future__ import annotations

import json
import os
import re
import sys
from pathlib import Path

import yaml


def read_agent(path: Path) -> dict | None:
    """The JSON summary at the end of an agent run's stdout (after the backend's messages)."""
    text = path.read_text() if path.exists() else ""
    i = text.rfind("\n{\n")
    if i < 0:
        return None
    try:
        return json.loads(text[i + 1:])
    except json.JSONDecodeError:
        return None


def judge(agent: dict | None, engine: dict | None, expect: str) -> dict:
    """One row's verdicts."""
    rec = (agent or {}).get("records") or []
    rec = rec[-1] if rec else {}
    top = ((engine or {}).get("candidates") or [])
    top1 = top[0] if top else None
    chosen = f"{rec.get('provider', '')}/{rec.get('machine_type', '')}/{rec.get('model', '')}" if rec and rec.get("machine_type") else ""
    # fits is set by ingest-agent's record check (older runs lack it): false when the offer
    # never came back from a match, so it is the closest thing named, not a placement.
    closest = bool(rec) and rec.get("fits") is False and bool(rec.get("machine_type"))
    if closest:
        chosen += " (closest, not a fit)"
    in_top = any(rec and c["provider"] == rec.get("provider") and c["machine_type"] == rec.get("machine_type") for c in top)
    verdict = ""
    if not agent:
        verdict = "no run"
    elif expect == "nothing":
        verdict = "right: declined" if not rec or not rec.get("machine_type") or closest else "widened"
    elif not rec:
        verdict = "no recommendation"
    elif engine is None:
        verdict = "answered (no baseline)"
    elif top1 and rec.get("provider") == top1["provider"] and rec.get("machine_type") == top1["machine_type"]:
        verdict = "agrees with engine"
    elif in_top:
        verdict = "in engine's top 10"
    else:
        verdict = "differs"
    # The cause: the engine's diagnosis for a request with no match, and whether the agent's
    # reason names the quota limit when that is the blocking constraint.
    why = (engine or {}).get("why_none") or {}
    blocking = [b["constraint"] for b in why.get("blocking") or []]
    cause = ""
    if expect == "nothing" and agent:
        named = "quota" in (rec.get("reason", "") + " " + rec.get("changes", "")).lower()
        cause = ("quota named" if named else "quota not named") if "nodes" in blocking else ("blocking: " + ", ".join(blocking) if blocking else "")
    usage = (agent or {}).get("usage") or {}
    return {"chosen": chosen, "engine_top": f"{top1['provider']}/{top1['machine_type']}/{top1['model']}" if top1 else "",
            "engine_candidates": len(top), "verdict": verdict, "cause": cause, "matches_used": (agent or {}).get("matches_used", 0),
            "proposals": len((agent or {}).get("proposals") or []), "turns": usage.get("turns", 0),
            "cost_usd": usage.get("cost_usd", 0.0), "wall_seconds": usage.get("wall_seconds", 0),
            "input_tokens": usage.get("input_tokens", 0), "cache_read_tokens": usage.get("cache_read_tokens", 0),
            "cache_write_tokens": usage.get("cache_write_tokens", 0), "output_tokens": usage.get("output_tokens", 0),
            "changes": rec.get("changes", "") if rec.get("changes", "") not in ("", "none", "None") else "",
            "reason": rec.get("reason", "")}


def collect(results: Path, requests: list[dict]) -> list[dict]:
    rows = []
    expect = {r["name"]: r.get("expect", "") for r in requests}
    for backend_dir in sorted(p for p in results.iterdir() if p.is_dir()):
        for model_dir in sorted(p for p in backend_dir.iterdir() if p.is_dir()):
            for out in sorted(model_dir.glob("*.agent.out")):
                tag = out.name[: -len(".agent.out")]
                name = re.sub(r"\.\d+$", "", tag)
                engine_path = model_dir / f"{name}.engine.json"
                engine = None
                if engine_path.exists():
                    try:
                        engine = json.loads(engine_path.read_text())
                    except json.JSONDecodeError:
                        engine = None
                row = {"backend": backend_dir.name, "model": model_dir.name, "request": tag}
                row.update(judge(read_agent(out), engine, expect.get(name, "")))
                if row["verdict"] == "no run":
                    err = out.with_suffix("").with_suffix(".agent.err")
                    text = err.read_text() if err.exists() else ""
                    m = re.search(r"(\w*(?:Exception|Error)\b[^\n]{0,160})", text)
                    if m:
                        row["verdict"] = "backend error"
                        row["reason"] = m.group(1).strip()
                rows.append(row)
    return rows


def markdown(rows: list[dict]) -> str:
    if not rows:
        return "No runs yet.\n"
    lines = ["# Ingest experiment", "", "Each row is one agent run against the deterministic engine's answer for the same request.", ""]
    for key in sorted({(r["backend"], r["model"]) for r in rows}):
        sub = [r for r in rows if (r["backend"], r["model"]) == key]
        ran = [r for r in sub if r["verdict"] not in ("no run", "backend error")]
        lines += [f"## {key[0]} / {key[1]}", ""]
        if ran:
            cost = sum(r["cost_usd"] for r in ran)
            agree = sum(1 for r in ran if r["verdict"] in ("agrees with engine", "right: declined"))
            lines += [f"{len(ran)} runs ({len(sub) - len(ran)} did not finish), {agree} agree with the engine, ${cost:.2f} in all "
                      f"(${cost / len(ran):.3f} a run), {sum(r['turns'] for r in ran) / len(ran):.1f} turns and "
                      f"{sum(r['wall_seconds'] for r in ran) / len(ran):.0f} s a run.", ""]
        else:
            lines += [f"{len(sub)} runs, none finished: {next((r['reason'] for r in sub if r['reason']), 'no output')}", ""]
        lines += [
                  "| Request | Agent chose | Engine's first | Verdict | Matches | Turns | $ | s |", "|---|---|---|---|---|---|---|---|"]
        for r in sub:
            lines.append(f"| {r['request']} | {r['chosen'] or '—'} | {r['engine_top'] or '—'} | {r['verdict']}{' (' + r['cause'] + ')' if r.get('cause') else ''} | {r['matches_used']} | {r['turns']} | {r['cost_usd']:.3f} | {r['wall_seconds']:.0f} |")
        lines.append("")
        for r in sub:
            if r["reason"]:
                lines.append(f"- **{r['request']}**: {r['reason']}" + (f" Changes: {r['changes']}" if r["changes"] else "")
                         + (f" Cause: {r['cause']}." if r.get("cause") else ""))
        lines.append("")
    return "\n".join(lines)


def summarize(rows: list[dict]) -> list[dict]:
    """Per backend and model directory (the arm): runs, agreement, cost and time."""
    out = []
    for key in sorted({(r["backend"], r["model"]) for r in rows}):
        sub = [r for r in rows if (r["backend"], r["model"]) == key]
        ran = [r for r in sub if r["verdict"] not in ("no run", "backend error")]
        k = max(len(ran), 1)
        out.append({"backend": key[0], "model": key[1].removesuffix("-flexible"),
                    "arm": "flexible" if key[1].endswith("-flexible") else "strict", "runs": len(sub), "finished": len(ran),
                    "agree": sum(1 for r in ran if r["verdict"] in ("agrees with engine", "right: declined")),
                    "cost_usd": round(sum(r["cost_usd"] for r in ran), 4), "cost_per_run": round(sum(r["cost_usd"] for r in ran) / k, 4),
                    "turns": round(sum(r["turns"] for r in ran) / k, 2), "seconds": round(sum(r["wall_seconds"] for r in ran) / k, 1)})
    return out


def site(results: Path, requests: list[dict], earlier: list[str], here: Path) -> dict:
    """The site's ingest experiment: findings, requests, and batches with rows and summaries."""
    findings = yaml.safe_load((here / "findings.yaml").read_text()) if (here / "findings.yaml").exists() else {}
    describe = {}
    for f in results.glob("*/*/*.engine.json"):
        try:
            describe.setdefault(f.name[: -len(".engine.json")], json.loads(f.read_text()).get("describe", ""))
        except json.JSONDecodeError:
            pass
    batches = []
    # Archived batches with a LABEL file, oldest first, then any named on the command line.
    labelled = [f"{(d / 'LABEL').read_text().strip()}={d}" for d in sorted((here / "archive").glob("*")) if (d / "LABEL").exists()]
    for spec in labelled + earlier:
        label, _, path = spec.partition("=")
        rows = collect(Path(path), requests)
        batches.append({"label": label, "rows": rows, "arms": summarize(rows)})
    rows = collect(results, requests)
    batches.append({"label": "latest", "rows": rows, "arms": summarize(rows)})
    for b in batches:
        for r in b["rows"]:
            r["arm"] = "flexible" if r["model"].endswith("-flexible") else "strict"
            r["name"] = re.sub(r"\.\d+$", "", r["request"])
    return {"question": findings.get("question", ""), "summary": " ".join(str(findings.get("summary", "")).split()), "findings": findings.get("findings", []), "next": findings.get("next", ""), "hypotheses": findings.get("hypotheses", []),
            "requests": [{"name": r["name"], "text": " ".join(r["text"].split()), "expect": r.get("expect", ""),
                          "jobspec": describe.get(r["name"], "")} for r in requests],
            "batches": batches}


def main(argv: list[str]) -> int:
    results = Path(argv[1] if len(argv) > 1 and not argv[1].startswith("--") else "results")
    here = Path(__file__).parent
    requests = yaml.safe_load((here / "requests.yaml").read_text())
    rows = collect(results, requests) if results.exists() else []
    if "--json" in argv:
        Path(argv[argv.index("--json") + 1]).write_text(json.dumps(rows, indent=2))
    if "--site" in argv:
        earlier = [argv[i + 1] for i, a in enumerate(argv) if a == "--earlier"]
        Path(argv[argv.index("--site") + 1]).write_text(json.dumps(site(results, requests, earlier, here), indent=1))
    print(markdown(rows), end="")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
