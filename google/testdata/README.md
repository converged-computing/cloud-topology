# Test fixtures

Synthetic data for tests and examples. The `test-*` machine types are not real
Google Cloud machine types, and `topology/test-gpu-2g/hwloc.xml` is an hwloc
synthetic topology with one GPU per socket and one NIC added by hand. Real data
lives in `data/`.

`quota-example/` is a separate small database with machine types shaped like real ones
(so quota rules apply) and a synthetic quota snapshot, for the quota tests and examples.

`plan-example/` covers capture planning: bare-metal and Local SSD variants, shared-core E2, a
family whose quota rule is inferred from the snapshot (C3A), one with no rule (U4C) and a GPU type.
