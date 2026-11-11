"""Soft labels from a model via behalf, and agreement with parsed labels."""

import json

from behalf import Task, ToolSpec

SOFT_VERSION = "1"

# Fields compared against parse.totals(); everything else is soft-only.
OVERLAP = ("pods", "gpus", "cpu_cores", "memory_gb")

SOFT_FIELDS = {
    "application": "main program or framework the workload runs (e.g. pytorch, lammps, "
                   "ray, a script name), or null",
    "software": "list of other software, libraries, or tools used",
    "launcher": "parallel launcher used (mpirun, torchrun, flux, ray, spark-submit, "
                "srun), or null",
    "containers": "list of container images used",
    "inputs": "list of datasets, buckets, or files the workload reads",
    "purpose": "one sentence describing what the workload does",
    "needs": "list of things a cluster must have for this to run that the file does not "
             "define: operators or custom resource kinds, secrets, config maps, volume "
             "claims, storage classes, node labels, GPUs",
    "pods": "total pods the workload runs at once, integer or null",
    "gpus": "total GPUs across all pods, integer or null",
    "cpu_cores": "total CPU cores requested across all pods, number or null",
    "memory_gb": "total memory requested across all pods in GiB, number or null",
}

SYSTEM_PROMPT = """You label Kubernetes workload manifests. Read every document in the
file, including the pod templates, container commands, resources, selectors, volumes
and environment. Call the record_labels tool exactly once with a JSON object
containing these keys:

{fields}

Use null (or an empty list) when the file does not determine a value. Do not guess
values that the file does not state or compute."""


class SoftLabelTask(Task):
    """Extract soft labels for one manifest."""

    name = "manifest-soft-label"

    def __init__(self):
        self.recorded = []

    def manifest_schema(self):
        """Manifest: the file text as the goal."""
        return {"goal": str}

    def build_tools(self, manifest):
        """A single action that records the labels."""

        async def record(args):
            labels = args.get("labels", args)
            if isinstance(labels, str):
                labels = json.loads(labels)
            self.recorded.append(labels)
            return {"content": [{"type": "text", "text": "recorded"}]}

        return [ToolSpec("record_labels", "Record the labels for this manifest.",
                         {"labels": dict}, record)]

    def execute_system_prompt(self, manifest):
        """Instructions listing the label fields."""
        fields = "\n".join(f"- {k}: {v}" for k, v in SOFT_FIELDS.items())
        return SYSTEM_PROMPT.format(fields=fields)

    def validate_result(self, result):
        """Require exactly one recorded label object."""
        if len(self.recorded) != 1 or not isinstance(self.recorded[0], dict):
            raise ValueError(f"expected one recorded label object, got {len(self.recorded)}")


def manifest_for(text):
    """Return the behalf manifest for a file."""
    return {"goal": f"Label this Kubernetes manifest:\n\n```yaml\n{text}\n```"}


def _norm(value):
    """Normalize a value for comparison: numbers to two decimals, else text."""
    if value is None or value == "":
        return None
    try:
        return round(float(value), 2)
    except (TypeError, ValueError):
        return str(value)


def agreement(hard_totals, soft):
    """Return {field: status} where status is match, mismatch, hard_only, soft_only, or none."""
    out = {}
    for field in OVERLAP:
        h, s = _norm(hard_totals.get(field)), _norm(soft.get(field))
        if h is None and s is None:
            out[field] = "none"
        elif h is None:
            out[field] = "soft_only"
        elif s is None:
            out[field] = "hard_only"
        else:
            out[field] = "match" if h == s else "mismatch"
    return out
