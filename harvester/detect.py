"""Detect Kubernetes workload manifests in text."""

import re

import yaml

# Workload kinds, keyed by API group and kind, with the controller a cluster needs
# installed before the kind can run (None for kinds the API server serves itself).
# A file is kept when one of its documents has one of these kinds. Other kinds
# (Secret, ConfigMap, PersistentVolumeClaim, Service, ...) are read by parse for
# the references a workload makes, but do not keep a file by themselves.
WORKLOADS = {
    ("", "Pod"): None,
    ("apps", "Deployment"): None,
    ("apps", "StatefulSet"): None,
    ("apps", "DaemonSet"): None,
    ("batch", "Job"): None,
    ("batch", "CronJob"): None,
    ("flux-framework.org", "MiniCluster"): "flux-operator",
    ("kubeflow.org", "MPIJob"): "training-operator",  # v2beta1 is the standalone mpi-operator
    ("kubeflow.org", "PyTorchJob"): "training-operator",
    ("kubeflow.org", "TFJob"): "training-operator",
    ("kubeflow.org", "XGBoostJob"): "training-operator",
    ("kubeflow.org", "PaddleJob"): "training-operator",
    ("kubeflow.org", "JAXJob"): "training-operator",
    ("trainer.kubeflow.org", "TrainJob"): "kubeflow-trainer",
    ("jobset.x-k8s.io", "JobSet"): "jobset",
    ("leaderworkerset.x-k8s.io", "LeaderWorkerSet"): "lws",
    ("ray.io", "RayJob"): "kuberay",
    ("ray.io", "RayCluster"): "kuberay",
    ("ray.io", "RayService"): "kuberay",
    ("argoproj.io", "Workflow"): "argo-workflows",
    ("argoproj.io", "CronWorkflow"): "argo-workflows",
    ("argoproj.io", "WorkflowTemplate"): "argo-workflows",
    ("batch.volcano.sh", "Job"): "volcano",
    ("sparkoperator.k8s.io", "SparkApplication"): "spark-operator",
    ("kubernetes.dask.org", "DaskCluster"): "dask-operator",
    ("kubernetes.dask.org", "DaskJob"): "dask-operator",
    ("serving.kserve.io", "InferenceService"): "kserve",
}

# Resource names that count as accelerators, by vendor.
ACCELERATORS = {
    "nvidia.com/gpu": "nvidia", "nvidia.com/mig": "nvidia", "amd.com/gpu": "amd",
    "intel.com/gpu": "intel", "habana.ai/gaudi": "habana", "google.com/tpu": "google",
    "aws.amazon.com/neuron": "aws", "aws.amazon.com/neuroncore": "aws",
}

HELM = re.compile(r"\{\{-?\s")
KIND_LINE = re.compile(r"^\s*kind:\s*[\"']?([A-Za-z]+)", re.M)
API_LINE = re.compile(r"^\s*apiVersion:\s*[\"']?([\w./-]+)", re.M)
MAX_ALIASES = 500  # a YAML with more alias references than this is not read (alias bombs)


def kind_name(group, kind):
    """Return the name a workload kind is stored under: core/Pod, batch/Job, ray.io/RayJob."""
    return f"{group or 'core'}/{kind}"


def split_api_version(api_version):
    """Return (group, version) for an apiVersion string."""
    if "/" in api_version:
        group, version = api_version.split("/", 1)
        return group, version
    return "", api_version


def documents(text):
    """Return the YAML documents of text that are mappings; raise yaml.YAMLError on failure."""
    if len(re.findall(r"\*\w", text)) > MAX_ALIASES:
        raise yaml.YAMLError("too many aliases")
    return [d for d in yaml.safe_load_all(text) if isinstance(d, dict)]


def workload_kind(doc):
    """Return the workload kind name of a parsed document, or None."""
    api_version, kind = doc.get("apiVersion"), doc.get("kind")
    if not isinstance(api_version, str) or not isinstance(kind, str):
        return None
    group, _ = split_api_version(api_version)
    if (group, kind) in WORKLOADS:
        return kind_name(group, kind)
    return None


def detect(text):
    """Return signal counts found in text: kept workload kinds and the reasons a file was not."""
    counts = {}

    def bump(key, n=1):
        counts[key] = counts.get(key, 0) + n

    # Cheap screen before parsing: every manifest names both.
    if "apiVersion" not in text or "kind" not in text:
        return counts
    helm = bool(HELM.search(text))
    if helm:
        bump("helm_template")
    try:
        docs = documents(text)
    except yaml.YAMLError:
        bump("yaml_error")
        # A Helm template is not valid YAML; record the kinds it names so a later
        # rendering stage can find it.
        if helm:
            for kind in KIND_LINE.findall(text):
                bump(f"helm:{kind}")
        return counts
    for doc in docs:
        bump("documents")
        name = workload_kind(doc)
        if name:
            bump(f"kind:{name}")
    for resource in ACCELERATORS:
        if resource in text:
            bump("accelerator")
            break
    if "kueue.x-k8s.io/queue-name" in text:
        bump("kueue")
    return counts


def managers(counts):
    """Return the sorted workload kind names with a confident signal in counts."""
    return sorted(key[len("kind:"):] for key in counts if key.startswith("kind:"))
