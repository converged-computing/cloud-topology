# Two clouds, one graph database. The per-cloud work lives in google/ and aws/; this
# file only combines them.
#
#   make graphdb            fold the AWS graphs, build the Google ones, load both into
#                           google/graphdb/graph.lbug (needs both CLIs configured)
#   make graphdb-query Q=…  run Cypher against it, e.g. Q=providers
#   make site               the web site into docs/: the published Google experiment, the
#                           AWS database with its captures, and one graph of both clouds
#   make serve              serve docs/ at http://localhost:8000
#
# GOOGLE_PROJECT and GOOGLE_REGION select the Google data; AWS_REGION the AWS region.
GOOGLE_PROJECT ?= llnl-flux
GOOGLE_REGION ?= us-central1
AWS_REGION ?= us-east-1
LEVELS ?= exact

.PHONY: graphdb graphdb-query site serve test

graphdb:
	$(MAKE) --no-print-directory -C aws folded REGION=$(AWS_REGION) LEVELS="$(LEVELS)"
	$(MAKE) --no-print-directory -C google graphdb PROJECT=$(GOOGLE_PROJECT) REGION=$(GOOGLE_REGION) GRAPHDB_LEVELS=$(subst $() ,$(,),$(LEVELS)) \
	  FOLDED="$(foreach f,$(wildcard aws/projects/*/folded/*-$(AWS_REGION).json),../$(f))"

graphdb-query:
	@$(MAKE) --no-print-directory -C google graphdb-query Q="$(Q)" $(if $(MODE),MODE=$(MODE))

site:
	$(MAKE) --no-print-directory -C aws folded REGION=$(AWS_REGION) LEVELS="exact"
	$(MAKE) --no-print-directory -C google site-final PROJECT=$(GOOGLE_PROJECT) REGION=$(GOOGLE_REGION) SITE=../docs

serve:
	python3 -m http.server --directory docs 8000

test:
	$(MAKE) -C google test
	$(MAKE) -C aws test

, := ,
