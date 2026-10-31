# Google Cloud Topology

Derive graphs for Google Cloud instances our (or more generally, an account) has access to.

## Usage

Open the folder in the devcontainer, which provides Go, Fluxion (resource-query and the Go
binding library), hwloc and gcloud. Then:

```bash
make test              # unit tests
make examples          # regenerate example graphs from testdata/
make run-examples      # match them with resource-query
make fluxion-go        # match them with Fluxion's Go bindings
```

To build the real dataset, driven by what your project's quota allows:

```bash
gcloud auth login --no-launch-browser --update-adc

export PROJECT=my-project
export REGION=us-central1
gcloud auth application-default set-quota-project $PROJECT
gcloud config set project llnl-flux

# public machine type catalog
make catalog REGIONS=$REGION

# snapshot this project's quotas
make quota REGIONS=$REGION   

# what the project can run, and how many
make plan REGION=$REGION

# one per series: the largest size quota allows
make plan-topology REGION=$REGION

# You need the topology agent here
pip install ./topology-agent[all] --break-system-packages

# I will pipe these into a file first and test the first one. E.g., claude.
claude auth login
make plan-topology REGION=$REGION | bash               # run them (each creates and deletes a VM)
make graphs REGION=$REGION                             # series, coarse and exact graphs sized by quota
make sizes REGION=$REGION SERIES=n2-standard VCPUS=16  # concrete sizes for a total
make serve                                                 # http://localhost:8080/v1/index
```

## Updating

Rerun `make catalog` to refresh machine types, and `make topology` to add or replace a topology.
Both rewrite `index.json`; `make index` rebuilds it alone. A running server reloads after
`kill -HUP <pid>`.

## License

MIT. See LICENSE, COPYRIGHT and NOTICE.
