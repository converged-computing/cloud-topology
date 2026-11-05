DATA ?= data
REGION ?= us-central1
QUOTA ?= projects/$(PROJECT)/quota.json
# Captures run through topology-agent (https://github.com/converged-computing/topology-agent).
TOPOLOGY_AGENT ?= topology-agent
BACKEND ?= claude
TOPOLOGY_AGENT_SOURCE ?= git+https://github.com/converged-computing/topology-agent@main
# pip needs this outside a virtualenv on Debian and Ubuntu images.
PIP_FLAGS ?= --break-system-packages

.PHONY: test vet build examples run-examples fluxion-go serve catalog index quota plan sizes plan-topology capture-all capture-agent experiment experiment-test report consistency consistency-report graphs topology images cleanup install-agent site site-final site-serve

test:
	go test ./...

vet:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w on the files above"; exit 1; }
	go vet ./...

# Build every command into bin/.
build:
	go build -o bin/ ./cmd/...

# Regenerate the example graphs from the test fixtures.
examples:
	go run ./cmd/graph --data testdata --level coarse --region us-central1 --nodes 4 --out examples/graphs/coarse-us-central1.json
	go run ./cmd/graph --data testdata --level exact --region us-central1 --nodes 2 --out examples/graphs/exact-us-central1.json
	go run ./cmd/graph --data testdata/quota-example --quota testdata/quota-example/quota.json --level series --region us-central1 --out examples/graphs/series-us-central1.json
	go run ./cmd/graph --data testdata/quota-example --level exact --region us-central1 --machine-types n2-standard-8 --nodes 2 --out examples/graphs/exact-n2-standard-8.json

# Match the example jobspecs with resource-query (in the devcontainer).
run-examples:
	bash examples/run.sh

# Match with Fluxion's Go bindings. The bindings build against the flux-sched checkout in
# FLUX_SCHED with cgo; the flags are set here unless the environment already has them.
FLUX_SCHED ?= /opt/flux-sched
export CGO_CFLAGS ?= -I$(FLUX_SCHED)
export CGO_LDFLAGS ?= -L$(FLUX_SCHED)/build/resource/reapi/bindings -lreapi_cli -lflux-idset -lstdc++ -ljansson -lhwloc -lboost_system -lflux-hostlist -lboost_graph -lyaml-cpp -Wl,-rpath,$(FLUX_SCHED)/build/resource/reapi/bindings
fluxion-go:
	cd examples/fluxion-go && go run . -graph ../graphs/exact-us-central1.json -jobspec ../jobspecs/exact-gpu-near-cores.yaml -count 5

serve:
	go run ./cmd/server --data $(DATA) $(if $(PROJECT),--quota $(QUOTA))

# Export machine types: make catalog PROJECT=my-project REGIONS=us-central1 FAMILIES=a3,g2
catalog:
	go run ./cmd/catalog --data $(DATA) --project $(PROJECT) --regions "$(REGIONS)" --families "$(FAMILIES)"

index:
	go run ./cmd/catalog --data $(DATA) --index-only

# Snapshot the project's quotas: make quota PROJECT=my-project REGIONS=us-central1
quota:
	go run ./cmd/quota fetch --project $(PROJECT) --regions "$(REGIONS)" --out $(QUOTA)

# What the project can run: make plan PROJECT=my-project REGION=us-central1
plan:
	go run ./cmd/quota plan --data $(DATA) --quota $(QUOTA) --region $(REGION) --models on-demand,flex-start

# Sizes in a series that satisfy totals: make sizes SERIES=n2-standard VCPUS=16 [GPUS=0 MEMORY_GB=0]
sizes:
	go run ./cmd/quota sizes --data $(DATA) $(if $(PROJECT),--quota $(QUOTA)) --region $(REGION) --series $(SERIES) --vcpus $(or $(VCPUS),0) --gpus $(or $(GPUS),0) --memory-gb $(or $(MEMORY_GB),0)

# Capture commands: the largest runnable size per series (review, then pipe to bash).
# Captures run cheapest first, on Spot where quota allows (ON_DEMAND=1 turns that off), each
# with its zones in order (the plan's region first). VERIFY=1 also captures each series'
# smallest size to check derived topologies. METAL=1 and SHARED_CORE=1 include bare-metal and
# shared-core types (skipped by default). RECAPTURE=1 plans series that are already captured.
plan-topology:
	@go run ./cmd/quota plan --data $(DATA) --quota $(QUOTA) --region $(REGION) --format commands $(if $(VERIFY),--verify) $(if $(METAL),--metal) $(if $(SHARED_CORE),--shared-core) $(if $(ON_DEMAND),--on-demand) $(if $(RECAPTURE),--recapture)

# Plan and run every capture, continuing past failures: make capture-all REGION=us-central1
# The plan and a per-capture log go to projects/$(PROJECT)/. Same options as plan-topology,
# plus run-plan.sh's RETRY_WAIT, RETRY_PASSES, STOP_AFTER_ERRORS and ALLOW_EXISTING.
capture-all:
	@mkdir -p projects/$(PROJECT)
	@$(MAKE) -s --no-print-directory plan-topology > projects/$(PROJECT)/captures.txt
	DATA=$(DATA) PROJECT=$(PROJECT) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) bash topology/run-plan.sh projects/$(PROJECT)/captures.txt projects/$(PROJECT)/captures.log

# Plan, then have the topology-agent agent work through it: make capture-agent REGION=us-central1
# BACKEND is claude, gemini or aws; MODEL picks the model (for claude an alias such as opus or
# sonnet, or a full model name); MAX_ATTEMPTS caps capture attempts in all (default 1 plus
# RETRY_PASSES per type); RETRY_PASSES and RETRY_WAIT give the agent the runner's patience.
# The agent's result goes to projects/$(PROJECT)/agent-result.json; images and the index are
# rebuilt afterwards whether or not every capture succeeded.
capture-agent:
	@mkdir -p projects/$(PROJECT)
	@$(MAKE) -s --no-print-directory plan-topology > projects/$(PROJECT)/captures.txt
	-$(TOPOLOGY_AGENT) run --plan projects/$(PROJECT)/captures.txt --out $(DATA) --backend $(BACKEND) $(if $(MODEL),--model $(MODEL)) --auto-approve $(if $(PROJECT),--project $(PROJECT)) $(if $(MAX_ATTEMPTS),--max-attempts $(MAX_ATTEMPTS)) $(if $(RETRY_PASSES),--retry-passes $(RETRY_PASSES)) $(if $(RETRY_WAIT),--retry-wait $(RETRY_WAIT)) > projects/$(PROJECT)/agent-result.json
	DATA=$(DATA) bash topology/render.sh
	go run ./cmd/catalog --data $(DATA) --index-only

# Compare the agent with the deterministic runner on the same plan: make experiment REGION=us-central1
# The plan covers every series quota allows, including those already captured, and each arm
# captures into its own database under $(EXPERIMENT); the comparison lands there as report.md
# and report.json. An existing plan is kept, so a rerun (or ARMS=deterministic) continues the
# same experiment. Takes plan-topology's options for a new plan, BACKEND, MODEL and
# MAX_ATTEMPTS for the agent arm, RETRY_WAIT, RETRY_PASSES and STOP_AFTER_ERRORS for the
# deterministic arm, and ARMS, FORCE and KEEP_LEFTOVERS (see topology/experiment.sh).
# LIMIT=N keeps only the first N capture lines of a new plan (the cheapest, as plans are
# sorted by cost).
EXPERIMENT ?= projects/$(PROJECT)/experiment
experiment:
	@mkdir -p $(EXPERIMENT)
	@test -s $(EXPERIMENT)/captures.txt || $(MAKE) -s --no-print-directory plan-topology RECAPTURE=1 | awk -v n='$(or $(LIMIT),0)' 'n && /^make topology /{ if (++i > n) next } { print }' > $(EXPERIMENT)/captures.txt
	DATA=$(DATA) PROJECT=$(PROJECT) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) BACKEND=$(BACKEND) bash topology/experiment.sh $(EXPERIMENT)/captures.txt $(EXPERIMENT)

# A small run to try the machinery before the real thing: the first LIMIT plan lines
# (default 3) into projects/$(PROJECT)/experiment-test, retrying stockouts after 5 minutes.
# Takes the same options as experiment; delete the directory to start over.
experiment-test:
	$(MAKE) --no-print-directory experiment EXPERIMENT=projects/$(PROJECT)/experiment-test LIMIT=$(or $(LIMIT),3) RETRY_WAIT=$(or $(RETRY_WAIT),300)

# Consistency study: capture the whole plan repeatedly over days, each round into its own
# database, to see whether each machine type's hardware is the same every time:
# make consistency REGION=us-central1 (ROUNDS=6, INTERVAL=43200: twice a day for three days).
# Rotates zones between rounds; a restarted study continues. The report is rebuilt after
# every round; EXTRA_DATA="main=data agent=projects/p/experiment/agent" adds other databases.
CONSISTENCY ?= projects/$(PROJECT)/consistency
consistency:
	@mkdir -p $(CONSISTENCY)
	@test -s $(CONSISTENCY)/captures.txt || $(MAKE) -s --no-print-directory plan-topology RECAPTURE=1 > $(CONSISTENCY)/captures.txt
	DATA=$(DATA) PROJECT=$(PROJECT) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) bash topology/consistency.sh $(CONSISTENCY)/captures.txt $(CONSISTENCY)

# Rebuild the consistency report from the rounds on disk (FORMAT=json for the JSON form).
consistency-report:
	@go run ./cmd/consistency --study $(CONSISTENCY) $(foreach d,$(EXTRA_DATA),--data $(d)) --format json > $(CONSISTENCY)/consistency.json
	@go run ./cmd/consistency --study $(CONSISTENCY) $(foreach d,$(EXTRA_DATA),--data $(d)) > $(CONSISTENCY)/consistency.md
	@cat $(CONSISTENCY)/consistency$(if $(filter json,$(FORMAT)),.json,.md)

# Rebuild the experiment's comparison from what is on disk, rewriting report.md and
# report.json there and printing the Markdown (FORMAT=json prints the JSON instead).
report:
	@go run ./cmd/report --plan $(EXPERIMENT)/captures.txt --dir $(EXPERIMENT) --format json > $(EXPERIMENT)/report.json
	@go run ./cmd/report --plan $(EXPERIMENT)/captures.txt --dir $(EXPERIMENT) > $(EXPERIMENT)/report.md
	@cat $(EXPERIMENT)/report$(if $(filter json,$(FORMAT)),.json,.md)

# Static web site to browse, search and compare databases, captures and graphs, for
# GitHub Pages or any static host: make site PROJECT=my-project REGION=us-central1
# SITE_DATA lists databases as label=dir (default main=$(DATA)); EXPERIMENTS lists experiment
# directories (name=dir or dir), whose arms and report are added; GRAPH_DATA limits graphs
# to these database labels (graphs for every arm and round add up); STUDY is a consistency study directory,
# whose rounds are added; LEVELS picks graphs (series,coarse,exact or none); SINGLE=1 writes
# one self-contained index.html instead of a directory.
SITE ?= site
SITE_DATA ?= main=$(DATA)
site:
	go run ./cmd/site --out $(SITE) $(foreach d,$(SITE_DATA),--data $(d)) $(foreach e,$(EXPERIMENTS),--experiment $(e)) $(if $(STUDY),--consistency $(STUDY)) $(foreach g,$(GRAPH_DATA),--graph-data $(g)) $(if $(PROJECT),--quota $(QUOTA)) $(if $(REGION),--region $(REGION)) $(if $(LEVELS),--levels $(LEVELS)) $(if $(SINGLE),--single)

# The published view: the final experiment run and the consistency study, graphs from the
# runner's arm. Older runs and data/ stay out (data/'s captures predate the current
# capture method).
site-final:
	$(MAKE) --no-print-directory site SITE_DATA= GRAPH_DATA=final/deterministic EXPERIMENTS=final=projects/$(PROJECT)/experiment STUDY=$(CONSISTENCY)

# Serve the site locally: http://localhost:8000
site-serve:
	python3 -m http.server --directory $(SITE) 8000

# Graphs sized by quota: make graphs PROJECT=my-project REGION=us-central1
# GRAPHS is the output directory, e.g. an experiment arm's: DATA=<arm> GRAPHS=<arm>.
GRAPHS ?= projects/$(PROJECT)
graphs:
	go run ./cmd/graph --data $(DATA) --quota $(QUOTA) --level series --region $(REGION) --out $(GRAPHS)/series-$(REGION).json
	go run ./cmd/graph --data $(DATA) --quota $(QUOTA) --level coarse --region $(REGION) --out $(GRAPHS)/coarse-$(REGION).json
	go run ./cmd/graph --data $(DATA) --quota $(QUOTA) --level exact --region $(REGION) --out $(GRAPHS)/exact-$(REGION).json

# Capture one topology: make topology MACHINE_TYPE=g2-standard-8 ZONE=us-central1-a ARGS="--maintenance-policy=TERMINATE"
# ZONE may be a comma-separated list, tried in order. --provisioning-model=SPOT in ARGS means
# Spot (SPOT_FALLBACK=0 stops it retrying on-demand); other ARGS pass through to the create.
topology:
	$(TOPOLOGY_AGENT) capture $(MACHINE_TYPE) --zones $(ZONE) --out $(DATA) $(if $(PROJECT),--project $(PROJECT)) $(if $(filter --provisioning-model=SPOT,$(ARGS)),--spot) $(if $(filter 0,$(SPOT_FALLBACK)),--no-spot-fallback) $(foreach a,$(filter-out --provisioning-model=SPOT,$(ARGS)),--arg=$(a)) $(if $(STATUS_FILE),--status-file $(STATUS_FILE))
	DATA=$(DATA) bash topology/render.sh $(MACHINE_TYPE)
	go run ./cmd/catalog --data $(DATA) --index-only

# Render topology.svg for every capture (FORCE=1 re-renders all).
images:
	DATA=$(DATA) bash topology/render.sh $(if $(FORCE),--force)

# List capture instances still running; DELETE=1 deletes them.
cleanup:
	$(TOPOLOGY_AGENT) cleanup $(if $(PROJECT),--project $(PROJECT)) $(if $(DELETE),--delete)

# Install topology-agent from git, or editable from a local checkout:
#   make install-agent                                    (TOPOLOGY_AGENT_SOURCE, default the git repo)
#   make install-agent TOPOLOGY_AGENT_SOURCE=../topology-agent
# With the claude backend, the Agent SDK's bundled Claude Code CLI is linked as `claude` next
# to python, for logging in.
install-agent:
	pip install $(PIP_FLAGS) $(if $(filter git+% http%,$(TOPOLOGY_AGENT_SOURCE)),"topology-agent[$(BACKEND)] @ $(TOPOLOGY_AGENT_SOURCE)",-e "$(TOPOLOGY_AGENT_SOURCE)[$(BACKEND)]")
	@cli=$$(python3 -c "import importlib.util, os; s = importlib.util.find_spec('claude_agent_sdk'); print(os.path.join(list(s.submodule_search_locations)[0], '_bundled', 'claude') if s else '')"); \
	  if [ -x "$$cli" ]; then ln -sf "$$cli" "$$(dirname "$$(command -v python3)")/claude" && echo "linked claude -> $$cli"; fi