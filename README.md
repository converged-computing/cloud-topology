# Cloud Topology

Derive graphs for cloud instances our (or more generally, an account) has access to.
High level, we want the graphs to mirror resources we have quota for. Quota does not guarantee capacity.

- `google/`: Google Cloud. The catalog, quota snapshot, hwloc captures, the three graph
  levels, the experiments and the site builder.
- `aws/`: AWS. The same catalog, quota snapshot, captures (143 of 146 series in
  us-east-1) and graphs from the EC2 API, in its own Go module that imports the
  provider-neutral packages of `google/`.
- `make graphdb` here folds both clouds' graphs into one LadybugDB;
  `make graphdb-query Q=providers` asks the same question of both.
- `make site` here builds the web site into `docs/`: the published Google experiment,
  the AWS database with its captures, and both clouds' graphs merged under one site
  root, `site0 › provider › region › …`, with a provider filter that prunes the rest.

## License

MIT. See LICENSE, COPYRIGHT and NOTICE.
