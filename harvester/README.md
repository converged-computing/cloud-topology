# harvester: Kubernetes manifests

Builds a corpus of Kubernetes workload manifests from public GitHub repositories, with a
pinned commit and license for every file: Jobs, Deployments, StatefulSets, MiniClusters,
MPIJobs, PyTorchJobs, JobSets, RayJobs, Argo Workflows, Volcano Jobs, Spark applications
and the rest of the kinds in `detect.py`. It is a copy of jobscript-harvester with the
detector, parser, soft-label task and search plan swapped for manifests; search,
repository metadata, scanning, storage and export are the same code, kept separate so the
two corpora can be built independently.

## Pipeline

```bash
pip install requests selenium pyyaml
python3 harvest.py search                 # discover repos via web code search in a logged-in browser
export GITHUB_TOKEN=...
python3 harvest.py repos                  # license, fork, archived, size per repo (REST API)
python3 harvest.py scan --workers 4       # shallow-clone each repo and detect manifests locally
python3 harvest.py label                  # deterministic labels: the scheduling subset
pip install "behalf[claude]"              # or [gemini] / [aws]
python3 harvest.py soft-label --backend claude --model <model> --limit 100
python3 harvest.py agree                  # compare parsed and model labels
python3 harvest.py export --out manifests.jsonl
python3 harvest.py stats
```

All stages are resumable; rerun a command to continue. `run-harvest.sh` alternates search
and scan. The database is `manifests.sqlite` and the browser profile
`~/.manifest-harvester-chrome`, so this and jobscript-harvester do not share state; log in
once in each profile.

**search** opens Chrome with a persistent profile; log in once in that window. The web
search is used because the REST code search runs on the legacy engine, which drops
punctuation from queries and only indexes repositories active in the last year.

**Discovery, not collection.** Search results only identify repositories. `scan` then reads
every file under `--max-bytes` (default 384 KB) at the default branch HEAD, so a repository
found through one MPIJob contributes all of its manifests, including the plain Jobs and
Deployments that are too common to search for.

## Headless, on a cloud instance

The default is the window above: a headed Chrome you log in to by hand. `--headless`
runs without one, for an instance that harvests unattended, and the login then comes
from one of two places:

```bash
# On the machine where the profile is already logged in:
python3 harvest.py cookies --out cookies.json      # the profile's github.com cookies, mode 600
                                                    # (if search holds the profile open, this opens a copy)
scp cookies.json instance:

# On the instance:
python3 harvest.py search --headless --cookies cookies.json --max-queries 3
rm cookies.json                                     # the profile keeps the session from here
python3 harvest.py stats
```

or, when there is no logged-in profile to copy from, or the session has expired:

```bash
python3 harvest.py login --headless                 # asks for the password, then each code
```

`login` fills the form and asks at the terminal for whatever GitHub wants next: an
authenticator or SMS code, or the new-device verification code it emails. A passkey
prompt is answered by choosing the authenticator app. A security key or passkey as the
only second factor cannot be used headless; export cookies from a laptop instead. GitHub
sessions last about two weeks.

Headless Chrome runs with `--password-store=basic` (no keyring on a server),
`--disable-dev-shm-usage`, and `--no-sandbox` only when running as root. `--delay MIN MAX`
sets the seconds waited after each page (default 1 to 3); a datacenter address may need
more before the secondary rate limit stops firing. The cookies file is a credential for the
account: copy it with scp, delete it after the first import, and consider a separate
GitHub account for harvesting. Neither the cookie handoff nor the terminal login has been
run against GitHub yet; the flow is tested against a simulated browser.

## Search plan (`plan.json`)

Each directive is a kind: `"kind: MiniCluster"`, `"kind: MPIJob"`, ... and for the
ambiguous ones a second term (`"kind: Job" "batch.volcano.sh"`, `"kind: Workflow"
"argoproj.io"`). Plain Jobs are searched only with `nvidia.com/gpu` or `parallelism:`,
since the kind alone has millions of hits and a query returns at most 100. A query that
returns 100 results is split by the next family: language, archived, extension, license,
then per-kind option terms (`slotsPerWorker`, `replicatedJobs`, `nvidia.com/gpu`,
`nodeSelector`, ...), software and HPC terms. The license family is removed at run time, as
in jobscript-harvester.

## Detection (`detect.py`)

A file is kept if it parses as YAML (or JSON) and one of its documents has an `apiVersion`
and `kind` in the workload table, which maps each kind to the controller a cluster needs
before it can run: nothing for core, apps and batch kinds; the Flux Operator, the Kubeflow
training operator or trainer, JobSet, LeaderWorkerSet, KubeRay, Argo Workflows, Volcano,
the Spark operator, the Dask operator, KServe for the others. Kinds are named by group,
`batch/Job` against `batch.volcano.sh/Job`. A Volcano Job and a batch Job are different
things to a cluster.

Signals that do not keep a file: `helm_template` and `helm:<Kind>` for Helm templates,
which are not valid YAML and need rendering before they can be read; `yaml_error`;
`accelerator` and `kueue` for files that mention one; `documents` for the count. Files
without the strings `apiVersion` and `kind` are not parsed at all. A file with more than
500 alias references is refused.

## Labels

**Hard labels** (`parse.py`, `label`), per workload document, read the way the API server
and each controller read them:

| Field | What it holds |
|---|---|
| `roles` | each pod template the workload runs and how many times: a Job's `parallelism` (default 1), a Deployment's `replicas`, an MPIJob's Launcher and Worker, a JobSet's replicated jobs times their parallelism, a RayCluster's head and worker groups, a LeaderWorkerSet's leaders and workers, a MiniCluster's `size`, a Spark driver and its executors, an Argo Workflow's templates with replicas unknown |
| per role `pod` | CPU in millicores and memory in MiB, requests and limits summed over containers, with requests defaulting to limits as the API server does; accelerators by resource name (`nvidia.com/gpu`, `amd.com/gpu`, `google.com/tpu`, ...); images; `nodeSelector`; which affinity kinds are set; topology spread count; tolerations; priority class, scheduler and runtime class; host network; volume kinds |
| `pods`, `cpu_millicores`, `memory_mib`, `gpus` | totals over roles, null when any role's replicas are unknown |
| `refs`, `undefined_refs` | Secrets, ConfigMaps, claims and service accounts the pods reference, and those the file does not define itself (a StatefulSet's claim templates define its claims) |
| `image_access` | per image: `public` for known public registries, `private` for ECR, localhost and addresses, `templated` for variables, `unknown` otherwise, until a registry check resolves it |
| `queue` | a Kueue queue label or a Volcano queue |
| `claims` | storage class and size of every claim the file defines |

Kind-specific fields ride along: `slots_per_worker` and `procs_per_node` (Kubeflow),
`completions` and `completion_mode` (Jobs), `min_replicas` and `max_replicas` (Ray),
`tasks` and `interactive` (MiniCluster), `min_available` (Volcano), `entrypoint` (RayJob),
`parallelism` (Argo). `parse.totals()` gives file-level values: pods, cores, GiB, GPUs,
images, whether all images are public, the count of undefined references, the controllers
needed, and whether any requests are set at all. Those are what the experiments select on:
a manifest with every image public, no undefined references and requests set can be run as
it is; one with an undefined Secret or a private image cannot.

Not read: resource names outside the accelerator table (`hugepages`, RDMA devices), Argo
`resources` on DAG tasks, Helm values, Kustomize overlays, and `nodeAffinity` on a
MiniCluster, which the Flux Operator expresses as a map rather than a selector.

**Soft labels** (`soft.py`, `soft-label`) come from a model through a behalf `Task` with
one `record_labels` tool: application, software, launcher, containers, inputs, purpose,
`needs` (what a cluster must have that the file does not define), plus pods, GPUs, cores
and GiB. The model sees the file only, never the parsed labels. `agree` stores per-field
status (`match`, `mismatch`, `hard_only`, `soft_only`, `none`) for the four numeric fields.

## Verification status

Tested here: detection and parsing on fixtures taken from the operators' own examples
(MPI operator pi, Kubeflow PyTorchJob, JobSet, KubeRay RayJob, Argo steps, Flux Operator
MiniCluster, Spark pi, Volcano MPI, Kubeflow Trainer); quantity formats from
`quantity.go`; image classification; a Helm template; a Compose file; an alias-heavy file.
The scan of `flux-framework/flux-operator` kept 70 files (64 MiniClusters, 6 Deployments)
with no parse errors; only 15 of the 70 set resource requests, which is the kind of gap the
`requests_set` total is for. The search stages are the same code as jobscript-harvester and
carry its verification; the per-kind queries and the per-directive option terms have not
been run against GitHub yet. Run `search --max-queries 3` first and check `stats`.

Soft labels: tested with a fake behalf runner; a live model run has not been done here.

## Notes

GitHub's Acceptable Use Policies allow research use of public, non-personal information
only if resulting publications are open access. Manifests can carry registry hosts, bucket
names, account ids in image paths and email annotations; scrub before deriving prompts.
