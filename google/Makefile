DATA ?= data
REGION ?= us-central1
QUOTA ?= projects/$(PROJECT)/quota.json
# Captures run through topology-agent (https://github.com/converged-computing/topology-agent).
TOPOLOGY_AGENT ?= topology-agent
BACKEND ?= claude
TOPOLOGY_AGENT_SOURCE ?= git+https://github.com/converged-computing/topology-agent@main
# pip needs this outside a virtualenv on Debian and Ubuntu images.
PIP_FLAGS ?= --break-system-packages

.PHONY: test vet build examples run-examples fluxion-go serve catalog index quota prices plan sizes plan-topology capture-all capture-agent $(EXPERIMENT_TARGETS) graphdb graphdb-query install-lbug site site-final site-serve graphs topology images cleanup install-agent install-ingest-agent

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

# Hourly On-Demand and Spot prices for the region's machine types from the Cloud Billing
# Catalog (read-only, no cost): make prices PROJECT=my-project
PRICES ?= projects/$(PROJECT)/prices.json
prices:
	go run ./cmd/prices --data $(DATA) --region $(REGION) --out $(PRICES)

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
	DATA=$(DATA) PROJECT=$(PROJECT) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) PROVIDER=$(PROVIDER) CLUSTER=$(CLUSTER) bash topology/run-plan.sh projects/$(PROJECT)/captures.txt projects/$(PROJECT)/captures.log

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

# Experiments (experiments/Makefile): the agent-versus-runner experiment, the consistency
# study and the GKE experiment. Variables such as PROJECT and REGION pass through.
EXPERIMENT_TARGETS := experiment experiment-test report consistency consistency-report gke gke-report fidelity fidelity-graphs fidelity-jobspecs fidelity-report ingest ingest-all
$(EXPERIMENT_TARGETS):
	@$(MAKE) --no-print-directory -C experiments $@

# Static web site to browse, search and compare databases, captures and graphs, for
# GitHub Pages or any static host: make site PROJECT=my-project REGION=us-central1
# SITE_DATA lists databases as label=dir (default main=$(DATA)); EXPERIMENTS lists experiment
# directories (name=dir or dir), whose arms and report are added; KUBERNETES lists runs of
# make gke (name=dir), whose gke/ database and reports are added; FIDELITY_RUNS lists
# scheduling-fidelity runs (make fidelity, name=dir); GRAPH_DATA limits graphs
# to these database labels (graphs for every arm and round add up); STUDY is a consistency study directory,
# whose rounds are added; LEVELS picks graphs (series,coarse,exact or none); SINGLE=1 writes
# one self-contained index.html instead of a directory.
SITE ?= site
SITE_DATA ?= main=$(DATA)
# FOLDED lists graphs folded elsewhere (aws/cmd/graph --fold), included under their own label;
# LABELS renames databases (old=new); HIDE keeps databases out of the lists.
site:
	go run ./cmd/site --out $(SITE) $(foreach d,$(SITE_DATA),--data $(d)) $(foreach f,$(FOLDED),--folded $(f)) $(foreach l,$(LABELS),--label $(l)) $(foreach h,$(HIDE),--hide $(h)) $(foreach e,$(EXPERIMENTS),--experiment $(e)) $(foreach k,$(KUBERNETES),--kubernetes $(k)) $(foreach f,$(FIDELITY_RUNS),--fidelity $(f)) $(foreach f,$(INGEST_RUNS),--ingest $(f)) $(if $(STUDY),--consistency $(STUDY)) $(foreach g,$(GRAPH_DATA),--graph-data $(g)) $(if $(PROJECT),--quota $(QUOTA)) $(if $(REGION),--region $(REGION)) $(if $(LEVELS),--levels $(LEVELS)) $(if $(SINGLE),--single)

# The published view: the final experiment run and the consistency study, graphs from the
# runner's arm. Older runs and data/ stay out (data/'s captures predate the current
# capture method). The deterministic arm is the published Compute Engine database and is
# labelled compute-engine, next to aws; the agent arm captured the same cloud again, so it
# is hidden from the database lists and reached from the experiment page.
# AGENT_MODEL picks which agent experiment is the published one.
AGENT_MODEL ?= claude-opus-5-5
# GKE runs under projects/$(PROJECT)/gke/<image>/gke are included as databases gke/<image>.
GKE_RUNS = $(foreach d,$(filter-out %/trial/gke/topology,$(wildcard projects/$(PROJECT)/gke/*/gke/topology)),gke/$(word 4,$(subst /, ,$(d)))=$(patsubst %/gke/topology,%,$(d)))
# The ingest experiment's report, when run.sh has written it.
INGEST_REPORT = $(if $(wildcard ../experiments/ingest/results/ingest.json),ingest=../experiments/ingest/results/ingest.json)
FIDELITY_DIRS = $(if $(wildcard projects/$(PROJECT)/fidelity/fidelity.json),fidelity=projects/$(PROJECT)/fidelity)
# AWS joins as the database "aws" with its folded graphs (make -C ../aws folded), when present.
# The site shows one graph, the exact level, so only that level is built and imported.
AWS_DATA ?= ../aws/data
AWS_FOLDED ?= $(wildcard ../aws/projects/*/folded/exact-*.json)
SITE_LABELS ?= $(AGENT_MODEL)/deterministic=compute-engine
SITE_HIDE ?= $(AGENT_MODEL)/agent
site-final:
	$(MAKE) --no-print-directory site LEVELS=exact SITE_DATA="$(if $(wildcard $(AWS_DATA)/index.json),aws=$(AWS_DATA))" FOLDED="$(AWS_FOLDED)" LABELS="$(SITE_LABELS)" HIDE="$(SITE_HIDE)" KUBERNETES="$(GKE_RUNS)" FIDELITY_RUNS="$(FIDELITY_DIRS)" INGEST_RUNS="$(INGEST_REPORT)" GRAPH_DATA=compute-engine EXPERIMENTS=$(AGENT_MODEL)=projects/$(PROJECT)/agent/$(AGENT_MODEL) STUDY=projects/$(PROJECT)/consistency

# Serve the site locally: http://localhost:8000
site-serve:
	python3 -m http.server --directory $(SITE) 8000


# Graph database: the folded graphs as Parquet tables loaded into LadybugDB for Cypher
# queries. GRAPHDB_DATA lists databases as label=dir (default the published agent
# experiment's deterministic arm and data/); the exact level by default.
#   make graphdb REGION=us-central1            export and load
#   make graphdb-query Q=shapes                run pkg/site/queries/shapes.cypher
#   make graphdb-query Q="MATCH (v:Vertex) RETURN v.type, count(*)"
GRAPHDB ?= graphdb
# FOLDED lists folded graphs from another provider (aws: make -C ../aws folded) to load
# into the same database, so one Cypher query spans both clouds.
GRAPHDB_DATA ?= vm=projects/$(PROJECT)/agent/$(AGENT_MODEL)/deterministic main=$(DATA)
GRAPHDB_LEVELS ?= exact
FOLDED ?=
graphdb:
	go run ./cmd/graphdb export --out $(GRAPHDB) $(foreach d,$(GRAPHDB_DATA),--data $(d)) $(foreach f,$(FOLDED),--folded $(f)) $(if $(PROJECT),--quota $(QUOTA)) $(if $(REGION),--region $(REGION)) --levels $(GRAPHDB_LEVELS)
	go run ./cmd/graphdb load --in $(GRAPHDB) --db $(GRAPHDB)/graph.lbug

graphdb-query:
	@go run ./cmd/graphdb query --db $(GRAPHDB)/graph.lbug $(if $(MODE),--mode $(MODE)) $(if $(wildcard pkg/site/queries/$(Q).cypher),--named $(Q),"$(Q)")

# Install the LadybugDB shell (lbug) that make graphdb uses.
LBUG_VERSION ?= v0.22.1
install-lbug:
	@arch=$$(uname -m | sed 's/x86_64/x86_64/; s/arm64/aarch64/'); tmp=$$(mktemp -d); \
	  curl -sL -o $$tmp/lbug.tar.gz https://github.com/LadybugDB/ladybug/releases/download/$(LBUG_VERSION)/lbug_cli-linux-$$arch.tar.gz && \
	  tar xzf $$tmp/lbug.tar.gz -C $$tmp && install -m 755 $$(find $$tmp -type f -name lbug) $(HOME)/.local/bin/lbug && echo "installed $$($(HOME)/.local/bin/lbug --version)"

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
# PROVIDER=gke captures on a node pool of the GKE cluster CLUSTER instead of a VM.
topology:
	$(TOPOLOGY_AGENT) capture $(MACHINE_TYPE) --zones $(ZONE) --out $(DATA) $(if $(PROJECT),--project $(PROJECT)) $(if $(PROVIDER),--provider $(PROVIDER)) $(if $(CLUSTER),--cluster $(CLUSTER)) $(if $(IMAGE_TYPE),--image-type $(IMAGE_TYPE)) $(if $(filter --provisioning-model=SPOT,$(ARGS)),--spot) $(if $(filter 0,$(SPOT_FALLBACK)),--no-spot-fallback) $(foreach a,$(filter-out --provisioning-model=SPOT,$(ARGS)),--arg=$(a)) $(if $(STATUS_FILE),--status-file $(STATUS_FILE))
	DATA=$(DATA) bash topology/render.sh $(MACHINE_TYPE)
	go run ./cmd/catalog --data $(DATA) --index-only

# Render topology.svg for every capture (FORCE=1 re-renders all).
images:
	DATA=$(DATA) bash topology/render.sh $(if $(FORCE),--force)

# List capture instances (with PROVIDER=gke CLUSTER=…, node pools) still running; DELETE=1 deletes them.
cleanup:
	$(TOPOLOGY_AGENT) cleanup $(if $(PROJECT),--project $(PROJECT)) $(if $(PROVIDER),--provider $(PROVIDER)) $(if $(CLUSTER),--cluster $(CLUSTER)) $(if $(DELETE),--delete)

# Install ingest-agent (make ingest) the same way: make install-ingest-agent [INGEST_AGENT_SOURCE=../../ingest-agent]
INGEST_AGENT_SOURCE ?= git+https://github.com/converged-computing/ingest-agent@main
install-ingest-agent:
	pip install $(PIP_FLAGS) $(if $(filter git+% http%,$(INGEST_AGENT_SOURCE)),"ingest-agent[$(BACKEND)] @ $(INGEST_AGENT_SOURCE)",-e "$(INGEST_AGENT_SOURCE)[$(BACKEND)]")

# Install topology-agent from git, or editable from a local checkout:
#   make install-agent                                    (TOPOLOGY_AGENT_SOURCE, default the git repo)
#   make install-agent TOPOLOGY_AGENT_SOURCE=../topology-agent
# With the claude backend, the Agent SDK's bundled Claude Code CLI is linked as `claude` next
# to python, for logging in.
install-agent:
	pip install $(PIP_FLAGS) $(if $(filter git+% http%,$(TOPOLOGY_AGENT_SOURCE)),"topology-agent[$(BACKEND)] @ $(TOPOLOGY_AGENT_SOURCE)",-e "$(TOPOLOGY_AGENT_SOURCE)[$(BACKEND)]")
	@cli=$$(python3 -c "import importlib.util, os; s = importlib.util.find_spec('claude_agent_sdk'); print(os.path.join(list(s.submodule_search_locations)[0], '_bundled', 'claude') if s else '')"); \
	  if [ -x "$$cli" ]; then ln -sf "$$cli" "$$(dirname "$$(command -v python3)")/claude" && echo "linked claude -> $$cli"; fi