DATA ?= data
REGION ?= us-central1
QUOTA ?= projects/$(PROJECT)/quota.json
# Captures run through topology-agent (https://github.com/converged-computing/topology-agent).
TOPOLOGY_AGENT ?= topology-agent
BACKEND ?= claude
TOPOLOGY_AGENT_SOURCE ?= git+https://github.com/converged-computing/topology-agent@main

.PHONY: test vet build examples run-examples fluxion-go serve catalog index quota plan sizes plan-topology capture-all capture-agent graphs topology images cleanup install-agent

test:
	go test ./...

vet:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w on the files above"; exit 1; }
	go vet ./...

build:
	go build ./...

# Regenerate the example graphs from the test fixtures.
examples:
	go run ./graph --data testdata --level coarse --region us-central1 --nodes 4 --out examples/graphs/coarse-us-central1.json
	go run ./graph --data testdata --level exact --region us-central1 --nodes 2 --out examples/graphs/exact-us-central1.json
	go run ./graph --data testdata/quota-example --quota testdata/quota-example/quota.json --level series --region us-central1 --out examples/graphs/series-us-central1.json
	go run ./graph --data testdata/quota-example --level exact --region us-central1 --machine-types n2-standard-8 --nodes 2 --out examples/graphs/exact-n2-standard-8.json

# Match the example jobspecs with resource-query (in the devcontainer).
run-examples:
	bash examples/run.sh

# Match with Fluxion's Go bindings (in the devcontainer).
fluxion-go:
	cd examples/fluxion-go && go run . -graph ../graphs/exact-us-central1.json -jobspec ../jobspecs/exact-gpu-near-cores.yaml -count 5

serve:
	go run ./server --data $(DATA) $(if $(PROJECT),--quota $(QUOTA))

# Export machine types: make catalog PROJECT=my-project REGIONS=us-central1 FAMILIES=a3,g2
catalog:
	go run ./catalog --data $(DATA) --project $(PROJECT) --regions "$(REGIONS)" --families "$(FAMILIES)"

index:
	go run ./catalog --data $(DATA) --index-only

# Snapshot the project's quotas: make quota PROJECT=my-project REGIONS=us-central1
quota:
	go run ./quota fetch --project $(PROJECT) --regions "$(REGIONS)" --out $(QUOTA)

# What the project can run: make plan PROJECT=my-project REGION=us-central1
plan:
	go run ./quota plan --data $(DATA) --quota $(QUOTA) --region $(REGION) --models on-demand,flex-start

# Sizes in a series that satisfy totals: make sizes SERIES=n2-standard VCPUS=16 [GPUS=0 MEMORY_GB=0]
sizes:
	go run ./quota sizes --data $(DATA) $(if $(PROJECT),--quota $(QUOTA)) --region $(REGION) --series $(SERIES) --vcpus $(or $(VCPUS),0) --gpus $(or $(GPUS),0) --memory-gb $(or $(MEMORY_GB),0)

# Capture commands: the largest runnable size per series (review, then pipe to bash).
# Captures run on-demand, cheapest first, each with its zones in order (the plan's region
# first). VERIFY=1 also captures each series' smallest size to check derived topologies.
# METAL=1 and SHARED_CORE=1 include bare-metal and shared-core types (skipped by default).
# SPOT=1 uses Spot where quota allows.
plan-topology:
	@go run ./quota plan --data $(DATA) --quota $(QUOTA) --region $(REGION) --format commands $(if $(VERIFY),--verify) $(if $(METAL),--metal) $(if $(SHARED_CORE),--shared-core) $(if $(SPOT),--spot)

# Plan and run every capture, continuing past failures: make capture-all REGION=us-central1
# The plan and a per-capture log go to projects/$(PROJECT)/. Same options as plan-topology.
capture-all:
	@mkdir -p projects/$(PROJECT)
	@$(MAKE) -s plan-topology > projects/$(PROJECT)/captures.txt
	bash topology/run-plan.sh projects/$(PROJECT)/captures.txt projects/$(PROJECT)/captures.log

# Plan, then have the topology-agent agent work through it: make capture-agent REGION=us-central1
# BACKEND is claude, gemini or aws; MODEL picks the model (for claude an alias such as opus or
# sonnet, or a full model name); MAX_ATTEMPTS caps capture attempts (default 2 per type).
# The agent's result goes to projects/$(PROJECT)/agent-result.json; images and the index are
# rebuilt afterwards whether or not every capture succeeded.
capture-agent:
	@mkdir -p projects/$(PROJECT)
	@$(MAKE) -s plan-topology > projects/$(PROJECT)/captures.txt
	-$(TOPOLOGY_AGENT) run --plan projects/$(PROJECT)/captures.txt --out $(DATA) --backend $(BACKEND) $(if $(MODEL),--model $(MODEL)) --auto-approve $(if $(PROJECT),--project $(PROJECT)) $(if $(MAX_ATTEMPTS),--max-attempts $(MAX_ATTEMPTS)) > projects/$(PROJECT)/agent-result.json
	DATA=$(DATA) bash topology/render.sh
	go run ./catalog --data $(DATA) --index-only

# Graphs sized by quota: make graphs PROJECT=my-project REGION=us-central1
graphs:
	go run ./graph --data $(DATA) --quota $(QUOTA) --level series --region $(REGION) --out projects/$(PROJECT)/series-$(REGION).json
	go run ./graph --data $(DATA) --quota $(QUOTA) --level coarse --region $(REGION) --out projects/$(PROJECT)/coarse-$(REGION).json
	go run ./graph --data $(DATA) --quota $(QUOTA) --level exact --region $(REGION) --out projects/$(PROJECT)/exact-$(REGION).json

# Capture one topology: make topology MACHINE_TYPE=g2-standard-8 ZONE=us-central1-a ARGS="--maintenance-policy=TERMINATE"
# ZONE may be a comma-separated list, tried in order. --provisioning-model=SPOT in ARGS means
# Spot (SPOT_FALLBACK=0 stops it retrying on-demand); other ARGS pass through to the create.
topology:
	$(TOPOLOGY_AGENT) capture $(MACHINE_TYPE) --zones $(ZONE) --out $(DATA) $(if $(PROJECT),--project $(PROJECT)) $(if $(filter --provisioning-model=SPOT,$(ARGS)),--spot) $(if $(filter 0,$(SPOT_FALLBACK)),--no-spot-fallback) $(foreach a,$(filter-out --provisioning-model=SPOT,$(ARGS)),--arg=$(a)) $(if $(STATUS_FILE),--status-file $(STATUS_FILE)) > /dev/null
	DATA=$(DATA) bash topology/render.sh $(MACHINE_TYPE)
	go run ./catalog --data $(DATA) --index-only

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
	pip install $(if $(filter git+% http%,$(TOPOLOGY_AGENT_SOURCE)),"topology-agent[$(BACKEND)] @ $(TOPOLOGY_AGENT_SOURCE)",-e "$(TOPOLOGY_AGENT_SOURCE)[$(BACKEND)]")
	@cli=$$(python -c "import importlib.util, os; s = importlib.util.find_spec('claude_agent_sdk'); print(os.path.join(list(s.submodule_search_locations)[0], '_bundled', 'claude') if s else '')"); \
	  if [ -x "$$cli" ]; then ln -sf "$$cli" "$$(dirname "$$(command -v python)")/claude" && echo "linked claude -> $$cli"; fi