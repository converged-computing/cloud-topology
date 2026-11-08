# The ingest experiment

Does an agent, given a request for compute in words, arrive at the answer the graphs
give, and what does that judgment cost? The deterministic engine (`ingest-agent match`)
is the baseline: a structured request becomes one Cypher query over the folded exact
graphs of every cloud and the offers come back ranked. The agent (`ingest-agent run`, a
[behalf](https://github.com/converged-computing/behalf) task) is handed the same request
as a user would say it, and has to read it, structure it, match it and recommend. Nothing
here touches a cloud; the model calls are the only cost.

## Question and hypotheses

- H1 (fidelity): for a request that fits, the agent's recommendation is the engine's
  first-ranked offer, or at least in its top ten, in most runs. Where it differs, the
  reason it records names something the engine's ranking does not weigh (a NIC layout,
  the number of zones, a topology that is captured rather than derived).
- H2 (structure): given words and a structured floor, the agent's proposal is the floor or
  a narrowing of it, never weaker; the floor check (`weaker()` in the task) never fires in
  a run that ends with a recommendation. Given words alone, the structured request it
  proposes carries every constraint the words state.
- H3 (nothing fits): when no offer can hold the request, the agent says so, or widens only
  along axes the user left open (another model, provider or region) and says that it did,
  instead of shrinking the request to make it fit.
- H4 (cost): one request costs cents, not dollars, and most of the cost on the Claude
  backend is the harness's own system prompt, which prompt caching amortizes across runs
  within an hour; on Bedrock the same run costs less per request because there is no
  harness prompt, and nothing is cached.

## Design

Arms, per request: the engine on the structured floor (the baseline, free), and the agent
on the words plus the floor. One request (`words-only`) has no floor, to test H2's second
half. Every request is a Flux jobspec (resources for what the graphs have vertices for,
`attributes.system.requires` for what they carry as properties), so the agent's proposal,
the engine's query and the jobspec a secretary would hand Fluxion are one document. The
request set is `requests.yaml`: five that fit (GPU training in a placement
group, a tight-coupled CPU job, a CPU batch, a big-memory node with block storage, a
single cheap GPU), one that cannot (four H100 nodes in one group, more than either
cloud's quota allows) and one in words only. Each entry carries what the engine says
(`expect`), so the report can judge the agent without a human.

The agent's authority is bounded in code, not in its instructions: every tool is
read-only; a proposal weaker than the floor in any dimension is refused with the
dimensions named; the matches are budgeted. `FLEXIBLE=1` runs a second arm in which the
floor is the totals rather than the shape (the agent may trade 2 nodes of 8 GPUs for 4 of
4, or drop a placement group) and every change is recorded, so the `impossible-gpu`
request has a legitimate answer; the report lists the changes. Runs are headless; the
agent's interactive mode (`ingest-agent run --interactive`, an `ask_user` tool to the
terminal) is for use by hand, not for the batch. The report reads those refusals and budgets
from the run's own summary. Backends: `claude` (the Claude Agent SDK, Opus 5.5 by
default) and `aws` (Strands on Amazon Bedrock); the same task, tools and prompts run on
both, so a difference between them is the harness and the model, not the experiment.

Metrics per run: the recommendation against the engine's ranking (agrees, in top ten,
differs, declined), proposals and matches used, turns, tokens (input, cached, output),
API time, wall time and cost (reported by the Claude backend; on Bedrock computed from
the tokens at list price).

## Where it lives

This directory, `experiments/ingest/` in cloud-topology, next to the graphs it queries; the
software is the [ingest-agent](https://github.com/converged-computing/ingest-agent)
library, installed separately. Run everything from this directory.

## Install

```bash
pip install -e "../../ingest-agent[aws]"      # the library with Strands, for Bedrock
pip install -e "../../ingest-agent[claude]"   # or the Claude Agent SDK backend
```

From the repository root, `make install-agents` installs both agent libraries.

The graph database comes from cloud-topology: `make graphdb` at its root folds the Google
and AWS exact graphs into `google/graphdb/graph.lbug` (it needs `lbug`, `make
install-lbug`). Prices come from `make -C aws prices` (the EC2 pricing API) and `make -C
google prices PROJECT=<project>` (the Cloud Billing Catalog), both read-only and free.
`run.sh` finds the database and both price files from here. Without the Google file every
Google offer is unpriced and the agent cannot compare the clouds on cost.

## Run

The whole experiment, unattended, is one script: `./batch.sh` starts it in the background
(strict arm, then flexible, three repeats each, then the report), `./batch.sh status`
shows progress and the report, `./batch.sh stop` stops it. Finished runs are skipped, so
starting it again resumes. To start over, keep the old runs or drop them first:

```bash
cd experiments/ingest
./batch.sh archive     # moves results/ to archive/<date-time>/ (gitignored), then:
./batch.sh             # a fresh batch
./batch.sh clean       # or delete results/ outright
```

The engine's answers (`*.engine.json`) are cached in `results/` too, so start over after
any change to the database, the prices or ingest-agent. For pieces of it:

```bash
cd experiments/ingest
REQUESTS="gpu-training" BACKEND=aws MODEL=us.anthropic.claude-opus-5 ./run.sh   # one request, Bedrock: the test
BACKEND=aws MODEL=us.anthropic.claude-opus-5 ./run.sh                           # every request, Bedrock
./run.sh                                        # every request, Claude backend, Opus 5.5
REPEAT=3 BACKEND=aws MODEL=us.anthropic.claude-opus-5 ./run.sh                  # three runs each
FLEXIBLE=1 BACKEND=aws MODEL=us.anthropic.claude-opus-5 ./run.sh                # the agent may reshape requests
python3 report.py results                       # the report, also written by run.sh
```

What a run touches: the local graph database (read through `lbug`) and the model endpoint
of the chosen backend (Bedrock's runtime API for `aws`). No other cloud resource is read
or created.

The Claude backend uses the Claude Code login (`claude` on the PATH, logged in) or
`ANTHROPIC_API_KEY`. The Bedrock backend uses the AWS credentials and region in the
environment; the model is a Bedrock model id or inference profile (`anthropic.claude-opus-5-5`
for the global endpoint; a region-prefixed profile such as `us.anthropic.claude-opus-5-5` where
the account requires one). Results go to `results/<backend>/<model>/`: for each request the
engine's answer (`.engine.json`), the agent's messages and summary (`.agent.out`) and its
log (`.agent.err`). A finished run is not repeated; delete its `.agent.out` to rerun it.

## Cost

Measured on 2026-11-08 with Claude Opus 5.5 through the Claude Agent SDK, the
`gpu-training` request with its floor, twice within an hour:

| | First run (cold cache) | Second run (warm cache) |
|---|---|---|
| Turns | 5 | 6 |
| Wall time | 27 s | 30 s |
| Input tokens | 11,149 | 11,206 |
| Cache written (1 h) | 68,663 | 19,355 |
| Cache read | 120,083 | 209,146 |
| Output tokens | 1,696 | 1,957 |
| Cost | $0.65 | $0.28 |

Both runs recommended the engine's first choice (p5.48xlarge, cluster group, $110.08 an
hour for the pair) with a correct reason. Most of the first run's cost is writing 68 K
tokens of the Claude Code harness prompt into the one-hour cache at $8 per million; the
request itself, our prompts, tool definitions and results, is a few thousand tokens a
turn. With the cache warm a run is about $0.25 to $0.30, so the seven-request set costs
about $2.50, and three repeats of it about $6.50.

On Amazon Bedrock the list price for Claude Opus 5.5 is the same as Anthropic's, $4 per
million input and $20 per million output tokens on the global endpoint (in-region
endpoints about 10% more), and there is no harness prompt: the agent sees only this
task's ~500 tokens of system prompt and tool definitions plus the conversation. A
five- or six-turn run sends about 15 to 25 K input tokens in all (each turn resends the
conversation so far, with match results of 1 to 2 K tokens and an offer layout of up to
3 K) and produces about 2 K output tokens, so one request is about $0.10 to $0.15 with no
caching at all, less than the same run through the Claude Agent SDK. Seven requests:
about $1; three repeats: about $3. Sonnet 5.5 halves both figures; Haiku 5.5 is about a
fortieth. Bedrock has no batch pricing for these
models, and the experiment would not use it anyway, since each run is interactive.
These Bedrock numbers are estimates from the measured token counts; `run.sh` with
`BACKEND=aws` records the actual tokens and prices them at list.

Measured on Bedrock with Claude Opus 5 (`us.anthropic.claude-opus-5`), 42 runs a batch:

| Batch | Cost | Per run | Time per run |
|---|---|---|---|
| First, before the tool fixes | $12.56 | $0.30 | 50 s |
| Second, tools fixed | $8.58 | $0.20 | 35 s |

The second batch is cheaper because the tools answer what the agent used to probe for.

## Results

What the runs mean is in [findings.yaml](findings.yaml), which the site's experiment
page shows next to the numbers. `run.sh` writes that page's data to
`results/ingest.json`; `make site` at the root picks it up. An archived batch with a
`LABEL` file (`./batch.sh archive "what changed"`) appears on the page beside the latest.

## First batch and what it changed

The first full batch (archived) agreed with the engine on every plain request, chose
hpc7g over the engine's c8gn for the tightly coupled job (cheaper in total, and EFA with
RDMA), and gave the same answer on all three repeats of every request. It also showed
four faults, all in the tools rather than the agent, now fixed in ingest-agent:

1. The match query stopped at 200 rows sorted by provider and name before ranking, so
   AWS zones filled the list and Google offers, and AWS types late in the alphabet
   (m6g, t4g, hpc7g.8xlarge), were never ranked. The cap now applies after ranking.
2. Google offers had no prices. `make -C google prices` now writes a snapshot in the AWS
   format, built from the per-vCPU, per-GB and per-GPU rates of each family.
3. When nothing matched, the agent guessed the cause and mostly guessed wrong (a 400 Gbps
   NIC, when the request filters on a node's total bandwidth). The match tool now returns
   `why_none`: the engine removes one constraint at a time and reports which ones alone
   block the request, with the quota's node limit. For impossible-gpu that is "at most 2
   p5 nodes in one placement group".
4. Recorded results were not checked: a GPU name in the provisioning-model field, a
   provider of "none". The record tool now checks provider, model and offer against the
   database, and sets `fits` itself: false when the offer never came back from a match,
   so a "closest thing" answer is not counted as a placement.

None of these changes the prompt for a particular request; each makes a tool return
what the database already knows. The report adds a cause check for requests expected to
fail: whether the agent's reason names the quota when the diagnosis says quota.
