# Experiments: everything here runs captures to answer a question and writes its results
# under projects/$(PROJECT)/; nothing here is needed to build or serve the database. Run
# from the google directory (make experiment …) or here (make -C experiments experiment …).
ROOT := $(abspath ..)
DATA ?= data
REGION ?= us-central1
QUOTA ?= projects/$(PROJECT)/quota.json
TOPOLOGY_AGENT ?= topology-agent
BACKEND ?= claude
# The agent's model names the agent experiment's variant: projects/$(PROJECT)/agent/$(MODEL).
MODEL ?= claude-opus-5-5
IMAGE_TYPE ?= COS_CONTAINERD

.PHONY: experiment experiment-test report consistency consistency-report gke gke-report fidelity fidelity-graphs fidelity-jobspecs fidelity-report

# Compare the agent with the deterministic runner on the same plan: make experiment REGION=us-central1
# The plan covers every series quota allows, including those already captured, and each arm
# captures into its own database under $(EXPERIMENT), one directory per agent model; the
# comparison lands there as report.md and report.json. An existing plan is kept, so a rerun
# (or ARMS=deterministic) continues the same experiment. Takes plan-topology's options for a new plan, BACKEND, MODEL and
# MAX_ATTEMPTS for the agent arm, RETRY_WAIT, RETRY_PASSES and STOP_AFTER_ERRORS for the
# deterministic arm, and ARMS, FORCE and KEEP_LEFTOVERS (see experiments/scripts/experiment.sh).
# LIMIT=N keeps only the first N capture lines of a new plan (the cheapest, as plans are
# sorted by cost).
EXPERIMENT ?= projects/$(PROJECT)/agent/$(MODEL)
experiment:
	@mkdir -p $(ROOT)/$(EXPERIMENT)
	@cd $(ROOT) && test -s $(EXPERIMENT)/captures.txt || $(MAKE) -s --no-print-directory plan-topology RECAPTURE=1 | awk -v n='$(or $(LIMIT),0)' 'n && /^make topology /{ if (++i > n) next } { print }' > $(EXPERIMENT)/captures.txt
	cd $(ROOT) && DATA=$(DATA) PROJECT=$(PROJECT) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) BACKEND=$(BACKEND) MODEL=$(MODEL) bash experiments/scripts/experiment.sh $(EXPERIMENT)/captures.txt $(EXPERIMENT)

# A small run to try the machinery before the real thing: the first LIMIT plan lines
# (default 3) into projects/$(PROJECT)/agent/test, retrying stockouts after 5 minutes.
# Takes the same options as experiment; delete the directory to start over.
experiment-test:
	cd $(ROOT) && $(MAKE) --no-print-directory experiment EXPERIMENT=projects/$(PROJECT)/agent/test LIMIT=$(or $(LIMIT),3) RETRY_WAIT=$(or $(RETRY_WAIT),300)

# Consistency study: capture the whole plan repeatedly over days, each round into its own
# database, to see whether each machine type's hardware is the same every time:
# make consistency REGION=us-central1 (ROUNDS=6, INTERVAL=43200: twice a day for three days).
# Rotates zones between rounds; a restarted study continues. The report is rebuilt after
# every round; EXTRA_DATA="main=data agent=projects/p/experiment/agent" adds other databases.
CONSISTENCY ?= projects/$(PROJECT)/consistency
consistency:
	@mkdir -p $(ROOT)/$(CONSISTENCY)
	@cd $(ROOT) && test -s $(CONSISTENCY)/captures.txt || $(MAKE) -s --no-print-directory plan-topology RECAPTURE=1 > $(CONSISTENCY)/captures.txt
	cd $(ROOT) && DATA=$(DATA) PROJECT=$(PROJECT) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) bash experiments/scripts/consistency.sh $(CONSISTENCY)/captures.txt $(CONSISTENCY)

# Rebuild the consistency report from the rounds on disk (FORMAT=json for the JSON form).
consistency-report:
	@cd $(ROOT) && go run ./experiments/cmd/consistency --study $(CONSISTENCY) $(foreach d,$(EXTRA_DATA),--data $(d)) --format json > $(CONSISTENCY)/consistency.json
	@cd $(ROOT) && go run ./experiments/cmd/consistency --study $(CONSISTENCY) $(foreach d,$(EXTRA_DATA),--data $(d)) > $(CONSISTENCY)/consistency.md
	@cd $(ROOT) && cat $(CONSISTENCY)/consistency$(if $(filter json,$(FORMAT)),.json,.md)

# Rebuild the experiment's comparison from what is on disk, rewriting report.md and
# report.json there and printing the Markdown (FORMAT=json prints the JSON instead).
report:
	@cd $(ROOT) && go run ./experiments/cmd/report --plan $(EXPERIMENT)/captures.txt --dir $(EXPERIMENT) --format json > $(EXPERIMENT)/report.json
	@cd $(ROOT) && go run ./experiments/cmd/report --plan $(EXPERIMENT)/captures.txt --dir $(EXPERIMENT) > $(EXPERIMENT)/report.md
	@cd $(ROOT) && cat $(EXPERIMENT)/report$(if $(filter json,$(FORMAT)),.json,.md)

# GKE experiment: capture the plan on node pools of a GKE cluster instead of VMs, to check
# that a node sees the same hardware and to measure what the Kubernetes path costs in time:
# make gke REGION=us-central1. IMAGE_TYPE is the node image (COS_CONTAINERD, the GKE default,
# or UBUNTU_CONTAINERD) and names the variant, projects/$(PROJECT)/gke/<image>; CLUSTER names the cluster (created in the plan's first zone and
# deleted at the end unless KEEP_CLUSTER=1). LIMIT=N runs the first N plan lines. The
# report compares shapes and provisioning times with VM captures in COMPARE (label=dir).
GKE ?= projects/$(PROJECT)/gke/$(shell echo $(IMAGE_TYPE) | tr A-Z a-z)
COMPARE ?= vm=projects/$(PROJECT)/agent/$(MODEL)/deterministic
gke:
	@mkdir -p $(ROOT)/$(GKE)
	@cd $(ROOT) && test -s $(GKE)/captures.txt || $(MAKE) -s --no-print-directory plan-topology RECAPTURE=1 | awk -v n='$(or $(LIMIT),0)' 'n && /^make topology /{ if (++i > n) next } { print }' > $(GKE)/captures.txt
	cd $(ROOT) && DATA=$(DATA) PROJECT=$(PROJECT) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) IMAGE_TYPE=$(IMAGE_TYPE) COMPARE="$(COMPARE)" bash experiments/scripts/gke.sh $(GKE)/captures.txt $(GKE)

# Rebuild the GKE experiment's reports: shape consistency and provisioning latency against COMPARE.
gke-report:
	@cd $(ROOT) && go run ./experiments/cmd/consistency --data gke=$(GKE)/gke $(foreach d,$(COMPARE),--data $(d)) --format json > $(GKE)/consistency.json
	@cd $(ROOT) && go run ./experiments/cmd/consistency --data gke=$(GKE)/gke $(foreach d,$(COMPARE),--data $(d)) > $(GKE)/consistency.md
	@cd $(ROOT) && go run ./experiments/cmd/latency --data gke=$(GKE)/gke $(foreach d,$(COMPARE),--data $(d)) --phases $(GKE)/phases.tsv --format json > $(GKE)/latency.json
	@cd $(ROOT) && go run ./experiments/cmd/latency --data gke=$(GKE)/gke $(foreach d,$(COMPARE),--data $(d)) --phases $(GKE)/phases.tsv | tee $(GKE)/latency.md
	@cat $(ROOT)/$(GKE)/consistency.md

# Scheduling fidelity: does the graph level change what Fluxion decides, and what does each
# level cost? Entirely local: graphs from the captures on disk, matched by resource-query.
# make fidelity PROJECT=my-project; VARIANTS and POLICIES narrow the run; a finished
# session is not rerun, so an interrupted run is finished by running it again.
FIDELITY ?= projects/$(PROJECT)/fidelity
FIDELITY_DB ?= projects/$(PROJECT)/agent/$(MODEL)/deterministic
FIDELITY_NODES ?= 1 2 4 8
fidelity-graphs:
	mkdir -p $(ROOT)/$(FIDELITY)/graphs
	cd $(ROOT) && for l in series coarse exact; do go run ./cmd/graph --data $(FIDELITY_DB) --quota projects/$(PROJECT)/quota.json --level $$l --region $(REGION) --out $(FIDELITY)/graphs/$$l.json || exit 1; done
	cd $(ROOT) && for n in $(FIDELITY_NODES); do go run ./cmd/graph --data $(FIDELITY_DB) --level exact --region $(REGION) --nodes $$n --out $(FIDELITY)/graphs/exact-n$$n.json || exit 1; done
	cd $(ROOT) && go run ./cmd/graph --data $(FIDELITY_DB) --quota projects/$(PROJECT)/quota.json --level exact --region $(REGION) --captured-only --out $(FIDELITY)/graphs/exact-captured.json
	cd $(ROOT) && go run ./experiments/cmd/fidelity ablate --in $(FIDELITY)/graphs/exact-n1.json --out $(FIDELITY)/graphs/exact-n1-no-nic.json --drop nic
fidelity-jobspecs:
	cd $(ROOT) && go run ./experiments/cmd/jobspecs --out $(FIDELITY)/jobspecs
fidelity: fidelity-jobspecs
	cd $(ROOT) && VARIANTS="$(VARIANTS)" POLICIES="$(POLICIES)" bash experiments/scripts/fidelity.sh $(FIDELITY)/graphs $(FIDELITY)/jobspecs $(FIDELITY)/runs
	$(MAKE) --no-print-directory fidelity-report
fidelity-report:
	cd $(ROOT) && go run ./experiments/cmd/fidelity report --runs $(FIDELITY)/runs --jobs $(FIDELITY)/jobspecs/jobs.tsv --graphs $(FIDELITY)/graphs --out $(FIDELITY) --captured $(FIDELITY_DB) --offers $(FIDELITY)/graphs/exact-n1.json
	@cat $(ROOT)/$(FIDELITY)/fidelity.md
