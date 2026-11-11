# Fluxion's queue manager over two resource backends: flux-sched's own resource module
# holding an exact JGF, and fluxion-graphdb (../../fluxion) answering from the graph
# database built from the same graph folded. Same workload, same RPCs, compared.
#
#   make graphs      the pair: graphs/exact.json and graphs/folded.json, then the database
#   make jobspecs    the fidelity workload (233 jobs) as exact-level jobspecs
#   make bench       both phases on both backends, into runs/
#   make compare     the tables, into results/
#   make qmanager    Fluxion's qmanager scheduling submitted jobs over the database
#   make profile     the database's queries one at a time, with ALLOCATIONS on record
#
# Every Flux step runs in its own single-broker instance (flux start -s1); nothing here
# touches a cloud.
ROOT := $(abspath ../..)
AWS_PROJECT ?= $(firstword $(notdir $(wildcard $(ROOT)/aws/projects/*)))
REGION ?= us-east-1
# The subset of instance types in the benchmark graph, and nodes per offer. Fluxion's
# module must hold the exact graph in memory, so this stays small; both backends see it.
FAMILIES ?= c7i m7i c6a m6g hpc7g g6 c7g
SIZES ?= large xlarge 2xlarge 4xlarge 8xlarge 12xlarge 16xlarge 24xlarge 48xlarge
MAX_NODES ?= 4
# Where the pair of graphs goes, and a tag on the run files, so a scaled-up graph can sit
# beside the default: make graphs bench compare GRAPHS=graphs-16 MAX_NODES=16 TAG=-16
GRAPHS ?= graphs
TAG ?=
TYPES := $(shell cd $(ROOT)/aws && ls data/machine-types | sed 's/\.json$$//' | grep -E "^($(subst $() ,|,$(FAMILIES)))\.($(subst $() ,|,$(SIZES)))$$" | paste -sd,)
FLUXION_SO ?= /opt/flux-sched/build/resource/modules/sched-fluxion-resource.so
QMANAGER_SO ?= /opt/flux-sched/build/qmanager/modules/sched-fluxion-qmanager.so
PHASES ?= single stream
# DRIVER=shell runs the database service through the lbug shell instead of the ladybug package.
DRIVER ?=

.PHONY: graphs db jobspecs bench compare qmanager profile clean

graphs:
	mkdir -p $(GRAPHS)
	cd $(ROOT)/aws && go run ./cmd/graph --data data --quota projects/$(AWS_PROJECT)/quota.json --level exact --region $(REGION) \
	  --machine-types "$(TYPES)" --max-nodes $(MAX_NODES) --out $(CURDIR)/$(GRAPHS)/exact.json --fold aws=$(CURDIR)/$(GRAPHS)/folded.json
	$(MAKE) db GRAPHS=$(GRAPHS)

db:
	mkdir -p $(GRAPHS)/db
	cd $(ROOT)/google && go run ./cmd/graphdb export --out $(CURDIR)/$(GRAPHS)/db --folded $(CURDIR)/$(GRAPHS)/folded.json
	cd $(ROOT)/google && go run ./cmd/graphdb load --in $(CURDIR)/$(GRAPHS)/db --db $(CURDIR)/$(GRAPHS)/db/graph.lbug

jobspecs:
	cd $(ROOT)/google && go run ./experiments/cmd/jobspecs --out $(CURDIR)/jobspecs

bench:
	@mkdir -p runs
	@for phase in $(PHASES); do for backend in fluxion graphdb; do \
	  echo "== $$backend $$phase"; \
	  ulimit -c 0; FLUXION_SO=$(FLUXION_SO) DRIVER=$(DRIVER) GRAPHS=$(CURDIR)/$(GRAPHS) flux start -s1 bash run.sh $$backend $$phase runs/$$backend-$$phase$(TAG).json 2>&1 | grep -v '^$$\|^Nov\|FATAL\|Aborted' | tail -1; \
	done; done

compare:
	@mkdir -p results
	@for phase in $(PHASES); do python3 compare.py runs/fluxion-$$phase$(TAG).json runs/graphdb-$$phase$(TAG).json --json results/$$phase$(TAG).json; echo; done | tee results/compare$(TAG).md

# qmanager over the database: load the stand-in and flux-sched's qmanager in one
# instance, submit the single-phase jobspecs, show what it decided. QUEUE_POLICY: fcfs,
# easy, conservative or hybrid.
QUEUE_POLICY ?= easy
qmanager:
	ulimit -c 0; QMANAGER_SO=$(QMANAGER_SO) QUEUE_POLICY=$(QUEUE_POLICY) flux start -s1 bash qmanager.sh 2>&1 | grep -v '^$$\|^Nov\|FATAL\|Aborted'

# One match's queries timed separately on a copy of the database (DB), with ALLOCATIONS
# synthetic allocations on record first; results/scaling.md collects these.
DB ?= $(GRAPHS)/db/graph.lbug
ALLOCATIONS ?= 0
profile:
	python3 profile.py --db $(DB) --allocations $(ALLOCATIONS)

clean:
	rm -rf runs results graphs-*
