"""Hard labels for Kubernetes workload manifests: the scheduling subset, read the way the
API server and each controller read it, plus what the file would need to run."""

import re

import yaml

from detect import ACCELERATORS, WORKLOADS, documents, kind_name, split_api_version

PARSER_VERSION = "1"

# Registries whose images are public unless the path says otherwise; registries that are
# private by construction; everything else is unknown until a registry check resolves it.
PUBLIC_HOSTS = {"docker.io", "ghcr.io", "quay.io", "gcr.io", "registry.k8s.io", "k8s.gcr.io",
                "nvcr.io", "public.ecr.aws", "mcr.microsoft.com", "docker.elastic.co"}
PRIVATE_HOST = re.compile(r"(\.dkr\.ecr\.|^localhost(:|$)|^\d+\.\d+\.\d+\.\d+(:|$)|\.internal(:|$))")
TEMPLATED = re.compile(r"[{}$%<>]")

QUANTITY = re.compile(r"^([+-]?\d*\.?\d+(?:[eE][+-]?\d+)?)([a-zA-Z]*)$")
SUFFIX = {"n": 1e-9, "u": 1e-6, "m": 1e-3, "": 1, "k": 1e3, "M": 1e6, "G": 1e9, "T": 1e12,
          "P": 1e15, "E": 1e18, "Ki": 2 ** 10, "Mi": 2 ** 20, "Gi": 2 ** 30, "Ti": 2 ** 40,
          "Pi": 2 ** 50, "Ei": 2 ** 60}
JVM_SUFFIX = {"": 2 ** 20, "k": 2 ** 10, "m": 2 ** 20, "g": 2 ** 30, "t": 2 ** 40}


def quantity(value):
    """Return a Kubernetes quantity as a float, or None (api/resource/quantity.go)."""
    if isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        return float(value)
    if not isinstance(value, str):
        return None
    match = QUANTITY.match(value.strip())
    if not match or match.group(2) not in SUFFIX:
        return None
    return float(match.group(1)) * SUFFIX[match.group(2)]


def millicores(value):
    """Return a CPU quantity in millicores, rounded, or None."""
    q = quantity(value)
    return None if q is None else int(round(q * 1000))


def mib(value):
    """Return a memory quantity in MiB, or None."""
    q = quantity(value)
    return None if q is None else q / 2 ** 20


def jvm_mib(value):
    """Return a Spark memory string (512m, 2g) in MiB, or None."""
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        return float(value)
    if not isinstance(value, str):
        return None
    match = re.match(r"^(\d+)([kKmMgGtT]?)$", value.strip())
    if not match:
        return None
    return int(match.group(1)) * JVM_SUFFIX[match.group(2).lower()] / 2 ** 20


def image_host(image):
    """Return the registry host of an image reference (docker.io when unnamed)."""
    first = image.split("/", 1)[0]
    if "/" in image and ("." in first or ":" in first or first == "localhost"):
        return first.lower()
    return "docker.io"


def image_access(image):
    """Return public, private, templated or unknown for an image reference."""
    if TEMPLATED.search(image):
        return "templated"
    host = image_host(image)
    if host in PUBLIC_HOSTS:
        return "public"
    if PRIVATE_HOST.search(host):
        return "private"
    return "unknown"


def _get(doc, *path, default=None):
    """Return doc[path...] when every step is a mapping key, else default."""
    cur = doc
    for key in path:
        if isinstance(cur, dict) and key in cur:
            cur = cur[key]
        else:
            return default
    return cur


def _list(value):
    return value if isinstance(value, list) else []


def _dict(value):
    return value if isinstance(value, dict) else {}


def _int(value, default=None):
    if isinstance(value, bool):
        return default
    if isinstance(value, int):
        return value
    if isinstance(value, str) and value.strip().isdigit():
        return int(value)
    return default


def _str(value):
    return value if isinstance(value, str) else None


def _names(items, *path):
    out = []
    for item in _list(items):
        name = _str(_get(item, *path))
        if name:
            out.append(name)
    return out


def empty_refs():
    return {"secrets": [], "configmaps": [], "pvcs": [], "serviceaccounts": []}


def container_resources(container):
    """Return requests and limits of one container, in millicores, MiB and accelerators."""
    res = _dict(container.get("resources"))
    out = {"cpu_request_m": None, "cpu_limit_m": None, "memory_request_mib": None,
           "memory_limit_mib": None, "accelerators": {}}
    requests, limits = _dict(res.get("requests")), _dict(res.get("limits"))
    out["cpu_request_m"] = millicores(requests.get("cpu"))
    out["cpu_limit_m"] = millicores(limits.get("cpu"))
    out["memory_request_mib"] = mib(requests.get("memory"))
    out["memory_limit_mib"] = mib(limits.get("memory"))
    # Extended resources must have limits; requests default to the limits.
    for name in ACCELERATORS:
        n = quantity(limits.get(name, requests.get(name)))
        if n:
            out["accelerators"][name] = int(n)
    return out


def _add(a, b):
    if a is None:
        return b
    if b is None:
        return a
    return a + b


def pod_labels(spec, refs):
    """Return the scheduling labels of one pod spec and add its references to refs."""
    spec = _dict(spec)
    containers = _list(spec.get("containers"))
    init = _list(spec.get("initContainers"))
    out = {"containers": len(containers), "init_containers": len(init),
           "cpu_request_m": None, "cpu_limit_m": None, "memory_request_mib": None,
           "memory_limit_mib": None, "accelerators": {}, "images": []}
    for c in containers:
        c = _dict(c)
        r = container_resources(c)
        for key in ("cpu_request_m", "cpu_limit_m", "memory_request_mib", "memory_limit_mib"):
            out[key] = _add(out[key], r[key])
        for name, n in r["accelerators"].items():
            out["accelerators"][name] = out["accelerators"].get(name, 0) + n
        if _str(c.get("image")):
            out["images"].append(c["image"])
        _container_refs(c, refs)
    for c in init:
        c = _dict(c)
        if _str(c.get("image")):
            out["images"].append(c["image"])
        _container_refs(c, refs)
    # A pod with limits and no requests gets requests equal to its limits.
    if out["cpu_request_m"] is None and out["cpu_limit_m"] is not None:
        out["cpu_request_m"] = out["cpu_limit_m"]
    if out["memory_request_mib"] is None and out["memory_limit_mib"] is not None:
        out["memory_request_mib"] = out["memory_limit_mib"]
    out["gpus"] = sum(out["accelerators"].values()) or None
    out["node_selector"] = _dict(spec.get("nodeSelector")) or None
    affinity = _dict(spec.get("affinity"))
    out["affinity"] = {"node": "nodeAffinity" in affinity, "pod": "podAffinity" in affinity,
                       "pod_anti": "podAntiAffinity" in affinity} if affinity else None
    out["topology_spread"] = len(_list(spec.get("topologySpreadConstraints"))) or None
    out["tolerations"] = [t for t in _list(spec.get("tolerations")) if isinstance(t, dict)] or None
    out["priority_class"] = _str(spec.get("priorityClassName"))
    out["scheduler_name"] = _str(spec.get("schedulerName"))
    out["runtime_class"] = _str(spec.get("runtimeClassName"))
    out["host_network"] = bool(spec.get("hostNetwork")) or None
    volumes = {"pvc": 0, "host_path": 0, "empty_dir": 0, "nfs": 0, "other": 0}
    for v in _list(spec.get("volumes")):
        v = _dict(v)
        if "persistentVolumeClaim" in v:
            volumes["pvc"] += 1
            claim = _str(_get(v, "persistentVolumeClaim", "claimName"))
            if claim:
                refs["pvcs"].append(claim)
        elif "hostPath" in v:
            volumes["host_path"] += 1
        elif "emptyDir" in v:
            volumes["empty_dir"] += 1
        elif "nfs" in v:
            volumes["nfs"] += 1
        elif "secret" in v:
            name = _str(_get(v, "secret", "secretName"))
            if name:
                refs["secrets"].append(name)
        elif "configMap" in v:
            name = _str(_get(v, "configMap", "name"))
            if name:
                refs["configmaps"].append(name)
        elif "projected" in v:
            for s in _list(_get(v, "projected", "sources")):
                for kind, bucket in (("secret", "secrets"), ("configMap", "configmaps")):
                    name = _str(_get(s, kind, "name"))
                    if name:
                        refs[bucket].append(name)
        else:
            volumes["other"] += 1
    out["volumes"] = {k: v for k, v in volumes.items() if v} or None
    refs["secrets"] += _names(spec.get("imagePullSecrets"), "name")
    account = _str(spec.get("serviceAccountName") or spec.get("serviceAccount"))
    if account and account != "default":
        refs["serviceaccounts"].append(account)
    return out


def _container_refs(container, refs):
    for env in _list(container.get("env")):
        src = _dict(_get(env, "valueFrom"))
        for kind, bucket in (("secretKeyRef", "secrets"), ("configMapKeyRef", "configmaps")):
            name = _str(_get(src, kind, "name"))
            if name:
                refs[bucket].append(name)
    for env in _list(container.get("envFrom")):
        for kind, bucket in (("secretRef", "secrets"), ("configMapRef", "configmaps")):
            name = _str(_get(env, kind, "name"))
            if name:
                refs[bucket].append(name)


def role(name, replicas, template, refs):
    """Return one role of a workload: a pod spec run replicas times."""
    spec = _get(template, "spec", default=template)
    return {"role": name, "replicas": replicas, "pod": pod_labels(spec, refs)}


def job_roles(name, job_spec, refs, multiplier=1):
    """Roles of a batch/v1 JobSpec; parallelism defaults to 1 (batch/v1 Job docs)."""
    job_spec = _dict(job_spec)
    parallelism = _int(job_spec.get("parallelism"), 1)
    r = role(name, parallelism * multiplier, _dict(job_spec.get("template")), refs)
    r["completions"] = _int(job_spec.get("completions"))
    r["completion_mode"] = _str(job_spec.get("completionMode"))
    return [r], job_spec


def replica_specs(spec, refs):
    """Roles of a Kubeflow *ReplicaSpecs map (MPIJob, PyTorchJob, TFJob, ...)."""
    roles = []
    for key, value in _dict(spec).items():
        if key.endswith("ReplicaSpecs") and isinstance(value, dict):
            for name, rs in value.items():
                rs = _dict(rs)
                roles.append(role(name, _int(rs.get("replicas"), 1), _dict(rs.get("template")), refs))
    return roles


def minicluster_roles(spec, refs):
    """Roles of a Flux Operator MiniCluster (api/v1alpha2/minicluster_types.go)."""
    spec = _dict(spec)
    pod = _dict(spec.get("pod"))
    containers = []
    for c in _list(spec.get("containers")):
        c = _dict(c)
        containers.append({"image": c.get("image"), "resources": c.get("resources")})
        secret = _str(c.get("imagePullSecret"))
        if secret:
            refs["secrets"].append(secret)
    synthetic = {"containers": containers, "nodeSelector": pod.get("nodeSelector"),
                 "tolerations": pod.get("tolerations"), "schedulerName": pod.get("schedulerName"),
                 "runtimeClassName": pod.get("runtimeClassName"),
                 "serviceAccountName": pod.get("serviceAccountName")}
    r = role("broker", _int(spec.get("size"), 1), synthetic, refs)
    r["tasks"] = _int(spec.get("tasks"))
    return [r]


def ray_roles(cluster, refs):
    """Roles of a RayCluster spec: one head and each worker group."""
    cluster = _dict(cluster)
    roles = [role("head", 1, _dict(_get(cluster, "headGroupSpec", "template")), refs)]
    for group in _list(cluster.get("workerGroupSpecs")):
        group = _dict(group)
        r = role(_str(group.get("groupName")) or "worker", _int(group.get("replicas"), 1),
                 _dict(group.get("template")), refs)
        r["min_replicas"], r["max_replicas"] = _int(group.get("minReplicas")), _int(group.get("maxReplicas"))
        roles.append(r)
    return roles


def argo_roles(spec, refs):
    """Roles of an Argo Workflow spec: each template with a container or script, replicas unknown."""
    roles = []
    for t in _list(_dict(spec).get("templates")):
        t = _dict(t)
        container = _dict(t.get("container") or t.get("script"))
        if container:
            synthetic = {"containers": [container], "nodeSelector": t.get("nodeSelector"),
                         "tolerations": t.get("tolerations"), "affinity": t.get("affinity"),
                         "volumes": t.get("volumes")}
            roles.append(role(_str(t.get("name")) or "template", None, synthetic, refs))
    return roles


def spark_roles(spec, refs):
    """Roles of a SparkApplication: driver and executors, with Spark's own cores and memory."""
    spec = _dict(spec)
    roles = []
    for name, count in (("driver", 1), ("executor", _int(_get(spec, "executor", "instances"), 1))):
        part = _dict(spec.get(name))
        image = part.get("image") or spec.get("image")
        resources = {"requests": {}, "limits": {}}
        cores = _int(part.get("cores"))
        if cores is not None:
            resources["requests"]["cpu"] = cores
        if _str(part.get("coreLimit")):
            resources["limits"]["cpu"] = part["coreLimit"]
        memory = jvm_mib(part.get("memory"))
        if memory is not None:
            resources["requests"]["memory"] = f"{int(memory)}Mi"
        gpu = _dict(part.get("gpu"))
        if _str(gpu.get("name")) and _int(gpu.get("quantity")):
            resources["limits"][gpu["name"]] = gpu["quantity"]
        synthetic = {"containers": [{"image": image, "resources": resources}],
                     "nodeSelector": part.get("nodeSelector") or spec.get("nodeSelector"),
                     "tolerations": part.get("tolerations"), "affinity": part.get("affinity"),
                     "serviceAccountName": part.get("serviceAccount")}
        roles.append(role(name, count, synthetic, refs))
    return roles


def trainjob_roles(spec, refs):
    """Roles of a Kubeflow Trainer TrainJob: numNodes pods of resourcesPerNode."""
    trainer = _dict(_dict(spec).get("trainer"))
    synthetic = {"containers": [{"image": trainer.get("image"),
                                 "resources": trainer.get("resourcesPerNode")}]}
    r = role("node", _int(trainer.get("numNodes"), 1), synthetic, refs)
    r["procs_per_node"] = trainer.get("numProcPerNode")
    r["runtime"] = _str(_get(spec, "runtimeRef", "name"))
    return [r]


def workload_roles(group, kind, doc, refs):
    """Return (roles, extra labels) for one workload document."""
    spec = _dict(doc.get("spec"))
    extra = {}
    if (group, kind) == ("", "Pod"):
        return [role("pod", 1, {"spec": spec}, refs)], extra
    if (group, kind) in (("apps", "Deployment"), ("apps", "StatefulSet")):
        return [role("pod", _int(spec.get("replicas"), 1), _dict(spec.get("template")), refs)], extra
    if (group, kind) == ("apps", "DaemonSet"):
        return [role("pod", None, _dict(spec.get("template")), refs)], extra
    if (group, kind) == ("batch", "Job"):
        roles, _ = job_roles("pod", spec, refs)
        extra = {"backoff_limit": _int(spec.get("backoffLimit")),
                 "active_deadline_seconds": _int(spec.get("activeDeadlineSeconds"))}
        return roles, extra
    if (group, kind) == ("batch", "CronJob"):
        roles, _ = job_roles("pod", _get(spec, "jobTemplate", "spec"), refs)
        extra = {"schedule": _str(spec.get("schedule"))}
        return roles, extra
    if group == "kubeflow.org":
        roles = replica_specs(spec, refs)
        extra = {"slots_per_worker": _int(spec.get("slotsPerWorker")),
                 "procs_per_node": spec.get("nprocPerNode"),
                 "run_policy": _dict(spec.get("runPolicy")) or None}
        return roles, extra
    if (group, kind) == ("trainer.kubeflow.org", "TrainJob"):
        return trainjob_roles(spec, refs), extra
    if (group, kind) == ("jobset.x-k8s.io", "JobSet"):
        roles = []
        for rj in _list(spec.get("replicatedJobs")):
            rj = _dict(rj)
            rs, _ = job_roles(_str(rj.get("name")) or "job", _get(rj, "template", "spec"), refs,
                              multiplier=_int(rj.get("replicas"), 1))
            rs[0]["job_replicas"] = _int(rj.get("replicas"), 1)
            roles += rs
        return roles, extra
    if (group, kind) == ("leaderworkerset.x-k8s.io", "LeaderWorkerSet"):
        replicas = _int(spec.get("replicas"), 1)
        lwt = _dict(spec.get("leaderWorkerTemplate"))
        size = _int(lwt.get("size"), 1)
        worker = _dict(lwt.get("workerTemplate"))
        leader = _dict(lwt.get("leaderTemplate")) or worker
        roles = [role("leader", replicas, leader, refs)]
        if size > 1:
            roles.append(role("worker", replicas * (size - 1), worker, refs))
        return roles, {"group_size": size}
    if (group, kind) == ("flux-framework.org", "MiniCluster"):
        return minicluster_roles(spec, refs), {"interactive": bool(spec.get("interactive")) or None}
    if (group, kind) == ("ray.io", "RayCluster"):
        return ray_roles(spec, refs), extra
    if (group, kind) == ("ray.io", "RayJob"):
        return ray_roles(spec.get("rayClusterSpec"), refs), {"entrypoint": _str(spec.get("entrypoint"))}
    if (group, kind) == ("ray.io", "RayService"):
        return ray_roles(spec.get("rayClusterConfig"), refs), extra
    if group == "argoproj.io":
        wf = _dict(spec.get("workflowSpec")) if kind == "CronWorkflow" else spec
        return argo_roles(wf, refs), {"parallelism": _int(wf.get("parallelism"))}
    if (group, kind) == ("batch.volcano.sh", "Job"):
        roles = [role(_str(t.get("name")) or "task", _int(t.get("replicas"), 1),
                      _dict(t.get("template")), refs) for t in _list(spec.get("tasks")) if isinstance(t, dict)]
        return roles, {"min_available": _int(spec.get("minAvailable")), "queue": _str(spec.get("queue"))}
    if (group, kind) == ("sparkoperator.k8s.io", "SparkApplication"):
        return spark_roles(spec, refs), extra
    if group == "kubernetes.dask.org":
        if kind == "DaskJob":
            cluster = _dict(_get(spec, "cluster", "spec"))
            roles = [role("job", 1, _dict(_get(spec, "job", "spec")), refs)]
        else:
            cluster, roles = spec, []
        roles.append(role("scheduler", 1, {"spec": _get(cluster, "scheduler", "spec")}, refs))
        worker = _dict(cluster.get("worker"))
        roles.append(role("worker", _int(worker.get("replicas"), 1), {"spec": worker.get("spec")}, refs))
        return roles, extra
    if (group, kind) == ("serving.kserve.io", "InferenceService"):
        predictor = _dict(spec.get("predictor"))
        model = _dict(predictor.get("model"))
        containers = _list(predictor.get("containers")) or ([model] if model else [])
        synthetic = {"containers": containers, "nodeSelector": predictor.get("nodeSelector"),
                     "tolerations": predictor.get("tolerations"), "affinity": predictor.get("affinity")}
        return [role("predictor", _int(predictor.get("minReplicas"), 1), synthetic, refs)], extra
    return [], extra


def workload(doc):
    """Return the labels of one workload document."""
    group, version = split_api_version(doc["apiVersion"])
    kind = doc["kind"]
    refs = empty_refs()
    roles, extra = workload_roles(group, kind, doc, refs)
    out = {"kind": kind, "group": group, "api_version": doc["apiVersion"],
           "name": _str(_get(doc, "metadata", "name")),
           "namespace": _str(_get(doc, "metadata", "namespace")),
           "controller": WORKLOADS[(group, kind)], "roles": roles}
    out.update(extra)
    pods = cpu = memory = gpus = None
    accelerators, images = {}, []
    for r in roles:
        pod, n = r["pod"], r["replicas"]
        images += pod["images"]
        if n is None:
            pods = None if pods is None and not roles else pods
            continue
        pods = _add(pods, n)
        if pod["cpu_request_m"] is not None:
            cpu = _add(cpu, pod["cpu_request_m"] * n)
        if pod["memory_request_mib"] is not None:
            memory = _add(memory, pod["memory_request_mib"] * n)
        for name, k in pod["accelerators"].items():
            accelerators[name] = accelerators.get(name, 0) + k * n
    if any(r["replicas"] is None for r in roles):
        pods = None
    gpus = sum(accelerators.values()) or None
    out.update({"pods": pods, "cpu_millicores": cpu, "memory_mib": memory, "gpus": gpus,
                "accelerators": accelerators or None,
                "accelerator_vendors": sorted({ACCELERATORS[a] for a in accelerators}) or None,
                "images": sorted(set(images)),
                "image_access": {i: image_access(i) for i in sorted(set(images))},
                "requests_set": cpu is not None or memory is not None})
    labels = _dict(_get(doc, "metadata", "labels"))
    if not out.get("queue"):
        out["queue"] = _str(labels.get("kueue.x-k8s.io/queue-name"))
    out["refs"] = {k: sorted(set(v)) for k, v in refs.items()}
    return out


DEFINING = {"Secret": "secrets", "ConfigMap": "configmaps",
            "PersistentVolumeClaim": "pvcs", "ServiceAccount": "serviceaccounts"}


def defined(docs):
    """Return the names of Secrets, ConfigMaps, claims and accounts the file itself defines."""
    out = {bucket: set() for bucket in DEFINING.values()}
    for doc in docs:
        kind = doc.get("kind")
        name = _str(_get(doc, "metadata", "name"))
        if kind in DEFINING and name:
            out[DEFINING[kind]].add(name)
        if kind == "StatefulSet":
            # volumeClaimTemplates make the claims a pod mounts by that name.
            for t in _list(_get(doc, "spec", "volumeClaimTemplates")):
                tname = _str(_get(t, "metadata", "name"))
                if tname:
                    out["pvcs"].add(tname)
    return out


def claims(docs):
    """Return the storage classes and sizes of claims the file defines."""
    out = []
    for doc in docs:
        specs = []
        if doc.get("kind") == "PersistentVolumeClaim":
            specs.append((_str(_get(doc, "metadata", "name")), _dict(doc.get("spec"))))
        for t in _list(_get(doc, "spec", "volumeClaimTemplates")):
            specs.append((_str(_get(t, "metadata", "name")), _dict(_get(t, "spec"))))
        for name, spec in specs:
            out.append({"name": name, "storage_class": _str(spec.get("storageClassName")),
                        "size_mib": mib(_get(spec, "resources", "requests", "storage")),
                        "access_modes": _list(spec.get("accessModes")) or None})
    return out


def parse(text, kinds):
    """Return {kind name: labels} for the workload kinds listed, as detect names them."""
    out = {}
    try:
        docs = documents(text)
    except yaml.YAMLError as err:
        return {k: {"workloads": [], "errors": [f"yaml: {err}"[:200]]} for k in kinds}
    have = defined(docs)
    file_claims = claims(docs)
    for name in kinds:
        workloads = []
        for doc in docs:
            api_version, kind = doc.get("apiVersion"), doc.get("kind")
            if not isinstance(api_version, str) or not isinstance(kind, str):
                continue
            group, _ = split_api_version(api_version)
            if kind_name(group, kind) != name or (group, kind) not in WORKLOADS:
                continue
            w = workload(doc)
            w["undefined_refs"] = {k: [n for n in v if n not in have[k]] for k, v in w["refs"].items()}
            w["undefined_refs"] = {k: v for k, v in w["undefined_refs"].items() if v}
            workloads.append(w)
        out[name] = {"workloads": workloads, "claims": file_claims, "errors": []}
    return out


def totals(labels):
    """Job-level values that follow from the labels, for comparison with soft labels."""
    ws = labels.get("workloads", [])
    pods = cpu = memory = gpus = None
    images, access, undefined, controllers = set(), set(), 0, set()
    for w in ws:
        pods, cpu, memory, gpus = (_add(pods, w["pods"]), _add(cpu, w["cpu_millicores"]),
                                   _add(memory, w["memory_mib"]), _add(gpus, w["gpus"]))
        images |= set(w["images"])
        access |= set(w["image_access"].values())
        undefined += sum(len(v) for v in w["undefined_refs"].values())
        if w["controller"]:
            controllers.add(w["controller"])
    if any(w["pods"] is None for w in ws):
        pods = None
    if "templated" in access or "private" in access:
        public = False
    elif images and access <= {"public"}:
        public = True
    else:
        public = None
    return {"workloads": len(ws), "pods": pods,
            "cpu_cores": None if cpu is None else round(cpu / 1000, 3),
            "memory_gb": None if memory is None else round(memory / 1024, 3),
            "gpus": gpus, "images": sorted(images), "images_public": public,
            "undefined_refs": undefined, "controllers": sorted(controllers),
            "requests_set": any(w["requests_set"] for w in ws) if ws else False}
