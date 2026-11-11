# Two clouds, one graph database. The per-cloud work lives in google/ and aws/; this
# file only combines them.
#
#   make graphdb            fold the AWS graphs, build the Google ones, load both into
#                           google/graphdb/graph.lbug (needs both CLIs configured)
#   make graphdb-query Q=…  run Cypher against it, e.g. Q=providers
#   make site               the web site into docs/: the published Google experiment, the
#                           AWS database with its captures, and one graph of both clouds
#   make serve              serve docs/ at http://localhost:8000
#   make ingest REQUEST=…   match a request (experiments/ingest/requests/*.yaml) against
#                           both clouds in the graph database with ingest-agent;
#                           make ingest-all runs them all; make install-agents installs
#                           topology-agent and ingest-agent
#   make install-fluxion    fluxion/ (Fluxion's resource service over the database) and
#                           ingest-agent into the interpreter flux python uses
#
# GOOGLE_PROJECT and GOOGLE_REGION select the Google data; AWS_REGION the AWS region.
GOOGLE_PROJECT ?= llnl-flux
GOOGLE_REGION ?= us-central1
AWS_REGION ?= us-east-1
# The Google database in the graph database: the published captures, labelled as on the site.
GOOGLE_DATABASE ?= google/projects/$(GOOGLE_PROJECT)/agent/claude-opus-5-5/deterministic
LEVELS ?= exact

.PHONY: graphdb graphdb-query site serve ingest ingest-all install-agents install-fluxion test

graphdb:
	$(MAKE) --no-print-directory -C aws folded REGION=$(AWS_REGION) LEVELS="$(LEVELS)"
	$(MAKE) --no-print-directory -C google graphdb PROJECT=$(GOOGLE_PROJECT) REGION=$(GOOGLE_REGION) GRAPHDB_LEVELS=$(subst $() ,$(,),$(LEVELS)) \
	  GRAPHDB_DATA="compute-engine=$(patsubst google/%,%,$(GOOGLE_DATABASE))" \
	  FOLDED="$(foreach l,$(LEVELS),$(foreach f,$(wildcard aws/projects/*/folded/$(l)-$(AWS_REGION).json),../$(f)))"

graphdb-query:
	@$(MAKE) --no-print-directory -C google graphdb-query Q="$(Q)" $(if $(MODE),MODE=$(MODE))

site:
	$(MAKE) --no-print-directory -C aws folded REGION=$(AWS_REGION) LEVELS="exact"
	$(MAKE) --no-print-directory -C google site-final PROJECT=$(GOOGLE_PROJECT) REGION=$(GOOGLE_REGION) SITE=../docs

serve:
	python3 -m http.server --directory docs 8000

ingest ingest-all:
	@$(MAKE) --no-print-directory -C google $@ $(if $(REQUEST),REQUEST=$(abspath $(REQUEST))) $(if $(FORMAT),FORMAT=$(FORMAT)) $(if $(TOP),TOP=$(TOP)) $(if $(RANK),RANK=$(RANK))

# The two agent libraries, editable from checkouts next to this file when present.
install-agents:
	$(MAKE) --no-print-directory -C google install-agent $(if $(wildcard google/topology-agent/pyproject.toml),TOPOLOGY_AGENT_SOURCE=topology-agent)
	$(MAKE) --no-print-directory -C google install-ingest-agent $(if $(wildcard ingest-agent/pyproject.toml),INGEST_AGENT_SOURCE=../ingest-agent)

# The Flux Python bindings live in the system interpreter, so the service does too; editable
# from the checkouts next to this file when present (ingest-agent is its own repository).
LADYBUG_VERSION ?= 0.22.1
PIP_FLAGS ?= --break-system-packages --user
install-fluxion:
	$(if $(wildcard ingest-agent/pyproject.toml),python3 -m pip install $(PIP_FLAGS) -e ingest-agent,python3 -m pip install $(PIP_FLAGS) "ingest-agent @ git+https://github.com/converged-computing/ingest-agent@main")
	python3 -m pip install $(PIP_FLAGS) "ladybug==$(LADYBUG_VERSION)" && python3 -m pip install $(PIP_FLAGS) --no-deps -e fluxion

test:
	$(MAKE) -C google test
	$(MAKE) -C aws test
	cd fluxion && PYTHONPATH=../ingest-agent python3 -m pytest -q

, := ,
