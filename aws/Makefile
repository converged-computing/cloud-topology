# AWS: the same database, quota snapshot and resource graphs as google/, for EC2.
# Credentials and the default region come from the AWS CLI configuration.
DATA ?= data
REGION ?= us-east-1
# PROJECT names the directory under projects/; the account id by default.
ACCOUNT := $(shell aws sts get-caller-identity --query Account --output text 2>/dev/null)
PROJECT ?= $(ACCOUNT)
QUOTA ?= projects/$(PROJECT)/quota.json
GRAPHS ?= projects/$(PROJECT)
FOLDED_DIR ?= $(GRAPHS)/folded

.PHONY: test vet build catalog index quota plan prices plan-topology topology capture-all cleanup budget graphs folded graphdb graphdb-query

test:
	go test ./...

vet:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w on the files above"; exit 1; }
	go vet ./...

build:
	go build -o bin/ ./cmd/...

# Export instance types and EBS volume types: make catalog REGION=us-east-1 [FAMILIES=c7i,p5]
# Previous-generation types are left out unless PREVIOUS=1.
catalog:
	go run ./cmd/catalog --data $(DATA) --region $(REGION) $(if $(FAMILIES),--families "$(FAMILIES)") $(if $(PREVIOUS),--include-previous)

index:
	go run ./cmd/catalog --data $(DATA) --index-only

# Snapshot the account's EC2 and EBS quotas: make quota REGION=us-east-1
quota:
	go run ./cmd/quota fetch --regions "$(REGION)" --out $(QUOTA)

# What the account can run (upper bounds: Service Quotas reports limits, not usage).
plan:
	go run ./cmd/quota plan --data $(DATA) --quota $(QUOTA) --region $(REGION) --models on-demand,spot

# Hourly On-Demand and Spot prices for the region's instance types, from the Pricing API
# and the Spot price history: make prices
PRICES ?= projects/$(PROJECT)/prices.json
prices:
	go run ./cmd/prices --data $(DATA) --region $(REGION) --out $(PRICES)

# Captures. Each runs through topology-agent's aws provider: one EC2 instance, launched
# with instance-initiated shutdown set to terminate and user data that schedules a
# shutdown after LIMIT_MINUTES before anything else, tagged purpose=topology-capture,
# Spot requests one-time. Deleting waits until EC2 reports the instance terminated.
TOPOLOGY_AGENT ?= topology-agent
LIMIT_MINUTES ?= 30

# The plan (review it, then make capture-all): the largest size per series the quota
# allows, Spot where possible, cheapest first, with the estimate in its header.
# PRICE_CAP (dollars per hour, default 25) leaves out expensive types unless they are in
# ALLOW_EXPENSIVE; METAL=1 adds bare-metal series; ON_DEMAND=1 avoids Spot; RECAPTURE=1
# plans series already captured.
PRICE_CAP ?= 25
plan-topology:
	@go run ./cmd/quota plan --data $(DATA) --quota $(QUOTA) --region $(REGION) --format commands --prices $(PRICES) --price-cap $(PRICE_CAP) $(if $(ALLOW_EXPENSIVE),--allow-expensive $(ALLOW_EXPENSIVE)) $(if $(METAL),--metal) $(if $(ON_DEMAND),--on-demand) $(if $(RECAPTURE),--recapture) --limit-minutes $(LIMIT_MINUTES)

# One capture: make topology MACHINE_TYPE=c7i.48xlarge ZONE=us-east-1a,us-east-1b [SPOT=1]
# ZONE is tried in order; SPOT=1 tries Spot first and On-Demand next in the same zone
# unless SPOT_FALLBACK=0. DRY_RUN=1 validates the launch with EC2 and creates nothing.
topology:
	$(TOPOLOGY_AGENT) capture $(MACHINE_TYPE) --provider aws --region $(REGION) --zones $(ZONE) --out $(DATA) --max-run $(LIMIT_MINUTES)m $(if $(SPOT),--spot) $(if $(filter 0,$(SPOT_FALLBACK)),--no-spot-fallback) $(if $(DRY_RUN),--dry-run) $(if $(STATUS_FILE),--status-file $(STATUS_FILE))
	$(if $(DRY_RUN),,DATA=$(DATA) bash ../google/topology/render.sh $(MACHINE_TYPE))
	$(if $(DRY_RUN),,go run ./cmd/catalog --data $(DATA) --index-only)

# Plan and run every capture, one at a time: make capture-all
# The plan and a per-capture log go to projects/$(PROJECT)/. Same options as plan-topology,
# plus run-plan.sh's DRY_RUN, RETRY_WAIT, RETRY_PASSES, SKIP_CAPTURED and ALLOW_EXISTING; failures
# join the retry set and never stop the run (STOP_AFTER_ERRORS=N to change that).
capture-all:
	@mkdir -p projects/$(PROJECT)
	@$(MAKE) -s --no-print-directory plan-topology > projects/$(PROJECT)/captures.txt
	DATA=$(DATA) REGION=$(REGION) TOPOLOGY_AGENT=$(TOPOLOGY_AGENT) bash capture/run-plan.sh projects/$(PROJECT)/captures.txt projects/$(PROJECT)/captures.log

# Capture instances still existing in the region; DELETE=1 terminates them.
cleanup:
	$(TOPOLOGY_AGENT) cleanup --provider aws --region $(REGION) $(if $(DELETE),--delete)

# A monthly cost budget with email alerts at 50, 80 and 100 percent, the backstop we do not
# run ourselves: make budget BUDGET_USD=300 BUDGET_EMAIL=you@example.com
BUDGET_USD ?= 300
budget:
	@test -n "$(BUDGET_EMAIL)" || { echo "set BUDGET_EMAIL"; exit 1; }
	aws budgets create-budget --account-id $(ACCOUNT) \
	  --budget '{"BudgetName":"topology-captures","BudgetLimit":{"Amount":"$(BUDGET_USD)","Unit":"USD"},"TimeUnit":"MONTHLY","BudgetType":"COST"}' \
	  --notifications-with-subscribers '[$(foreach t,50 80 100,{"Notification":{"NotificationType":"ACTUAL","ComparisonOperator":"GREATER_THAN","Threshold":$(t),"ThresholdType":"PERCENTAGE"},"Subscribers":[{"SubscriptionType":"EMAIL","Address":"$(BUDGET_EMAIL)"}]}$(if $(filter-out 100,$(t)),$(,)))]'

, := ,

# Graphs sized by quota: make graphs REGION=us-east-1
# The exact graph on disk is capped at EXACT_MAX_NODES nodes per offer: with EC2's class
# quotas nearly every type would get the 64-node cap, and 1,200 types x 6 zones x 64 nodes
# is too large a file for anything to load. The folded graphs (make folded) carry the full
# quota-sized counts without materializing them.
EXACT_MAX_NODES ?= 4
MODELS ?= on-demand
graphs:
	go run ./cmd/graph --data $(DATA) --quota $(QUOTA) --level series --region $(REGION) --models $(MODELS) --out $(GRAPHS)/series-$(REGION).json
	go run ./cmd/graph --data $(DATA) --quota $(QUOTA) --level coarse --region $(REGION) --models $(MODELS) --out $(GRAPHS)/coarse-$(REGION).json
	go run ./cmd/graph --data $(DATA) --quota $(QUOTA) --level exact --region $(REGION) --models $(MODELS) --max-nodes $(EXACT_MAX_NODES) --out $(GRAPHS)/exact-$(REGION).json

# Folded graphs for the graph database, one file per level. The repository's top-level
# make graphdb loads them with the Google graphs into one LadybugDB.
LEVELS ?= series coarse exact
folded:
	@for level in $(LEVELS); do \
	  go run ./cmd/graph --data $(DATA) --quota $(QUOTA) --level $$level --region $(REGION) --models $(MODELS) --fold aws=$(FOLDED_DIR)/$$level-$(REGION).json || exit 1; \
	done

# Only the AWS graphs in a LadybugDB, for queries on this cloud alone.
GRAPHDB ?= graphdb
graphdb: folded
	cd ../google && go run ./cmd/graphdb export --out ../aws/$(GRAPHDB) $(foreach f,$(wildcard $(FOLDED_DIR)/*.json),--folded ../aws/$(f))
	cd ../google && go run ./cmd/graphdb load --in ../aws/$(GRAPHDB) --db ../aws/$(GRAPHDB)/graph.lbug

graphdb-query:
	@cd ../google && go run ./cmd/graphdb query --db ../aws/$(GRAPHDB)/graph.lbug $(if $(MODE),--mode $(MODE)) $(if $(wildcard ../google/pkg/site/queries/$(Q).cypher),--named $(Q),"$(Q)")
