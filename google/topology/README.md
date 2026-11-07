# topology

Capturing a machine type's topology is done by
[topology-agent](https://github.com/converged-computing/topology-agent): it creates an
instance, reads its layout with hwloc, writes `data/topology/<machine-type>/hwloc.xml` and
`capture.json`, and deletes the instance. This directory holds what this repository adds around
it: running a whole plan (`run-plan.sh`) and rendering images (`render.sh`).

## Installing topology-agent

The devcontainer installs it into `/opt/topology-agent` from the `TOPOLOGY_AGENT_SOURCE` build
argument in `.devcontainer/devcontainer.json` (the git repository by default; set it to `""` to
skip). To install or reinstall it yourself, from git or from a local checkout (editable):

```bash
make install-agent
make install-agent TOPOLOGY_AGENT_SOURCE=../topology-agent
```

The Makefile calls it as `$(TOPOLOGY_AGENT)` (default `topology-agent`), so
`TOPOLOGY_AGENT="python -m topology_agent"` also works.

With the `claude` backend, the Claude Agent SDK includes the Claude Code CLI; both the
devcontainer and `make install-agent` link it as `claude`. Log in once for agent runs:

```bash
claude                  # then /login, open the link in your browser and paste the code
claude setup-token      # or: a one-year token to export as CLAUDE_CODE_OAUTH_TOKEN
```

The devcontainer keeps `~/.claude` on a named volume, so the login survives rebuilds.

## One capture

```bash
make topology MACHINE_TYPE=g2-standard-8 ZONE=us-central1-a,us-central1-b ARGS="--maintenance-policy=TERMINATE"
```

`ZONE` is a comma-separated list tried in order: a stockout or quota error moves on to the next
zone, and any other error stops. `PROVIDER=gke CLUSTER=name` captures on a one-node node pool
of that GKE cluster instead of a VM (see `experiments/README.md`). `--provisioning-model=SPOT` in `ARGS` means Spot, retrying
on-demand in the same zone unless `SPOT_FALLBACK=0`; other `ARGS` pass through to the create.
`STATUS_FILE=path` writes the outcome (`ok`, `stockout`, `quota` or `error`) to a file.
After a capture the topology image is rendered and `index.json` rebuilt. Every instance carries
the label `purpose=topology-capture` and a hard 1-hour limit after which Google deletes it.

```bash
make cleanup             # capture instances still running
make cleanup DELETE=1    # delete them
```

## A whole plan

Two ways to run the plan from `make plan-topology`:

```bash
make capture-all REGION=us-central1      # deterministic runner (run-plan.sh)
make capture-agent REGION=us-central1    # the topology-agent agent (BACKEND=claude|gemini|aws)
```

`capture-all` runs each line with `make topology` and logs its outcome to
`projects/<project>/captures.log`, one tab-separated line per attempt: time, machine type,
`ok`, `stockout`, `quota` or `error`, seconds, pass number and, for errors, the message.
Captures that stocked out in every zone are retried after `RETRY_WAIT` seconds (default
1800), in up to `RETRY_PASSES` more passes (default 2); quota failures are not retried. It
stops after `STOP_AFTER_ERRORS` consecutive errors that are not about capacity (default 5),
where an error repeating the previous message counts once, so a run of machine types that
all fail the same way does not end the plan. It refuses to start if a capture instance
already exists (`ALLOW_EXISTING=1` overrides), and holds a lock next to the plan so two
runs cannot overlap. It exits 0 only when every line captured.

`capture-agent` hands the same plan to the agent, which captures each machine type through a
single bounded tool: only the plan's machine types and zones (a substitute size or series is
refused in code, not just by instruction), an attempt budget (`MAX_ATTEMPTS`, default one
first try plus `RETRY_PASSES` retries per machine type), and an allowlist of extra create
flags. The agent gets the runner's patience: `RETRY_PASSES` retries per stocked-out type,
each after a `wait_for_capacity` of up to `RETRY_WAIT` seconds. Captures are auto-approved
and each approval is logged. Its result goes to
`projects/<project>/agent-result.json`, and images and the index are rebuilt afterwards.

Plans capture on Spot where quota allows; `make plan-topology ON_DEMAND=1` (or
`capture-all ON_DEMAND=1`) captures on-demand only. Each line lists every zone that offers
the machine type, the plan's region first, then regions in the same geography, then the
rest, so a stockout moves on to the next zone. Zones come from the catalog, so rerun
`make catalog` with more regions (or none, for all) to let captures fail over to other
regions. GPU and Local SSD machine types get `--maintenance-policy=TERMINATE`, which Compute
Engine requires for them. Arm machine types (t2a, c4a, n4a) boot the arm64 build of the
Ubuntu image; topology-agent looks up the architecture before creating the instance.

## Experiments

The experiments that use these captures, comparing the agent with the runner, repeating
captures over days, and capturing on GKE, live in `experiments/`; see its README.

## Images

Each capture is rendered as `topology.svg` by `render.sh`, using hwloc's `lstopo-no-graphics`
with a legend from `capture.json` (machine type, capture date, provisioning model, hwloc
version). `make images` renders captures that have no image or a newer capture;
`make images FORCE=1` re-renders all. The images are stable across re-renders: lstopo's own
date stamp is dropped from the legend, and the `viewBox` is rewritten without units, since
lstopo writes `px` units there, which some renderers reject.
