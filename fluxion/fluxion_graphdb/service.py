"""The Flux side: register sched-fluxion-resource on this instance and answer qmanager.

Fluxion's queue manager (sched-fluxion-qmanager) never touches the resource graph. It asks
the service named sched-fluxion-resource over RPCs: match (or the streaming match_multi)
with a jobspec, which answers ALLOCATED with R and the start time, RESERVED with a future
start, or fails EBUSY or ENODEV; cancel when a job ends; update, info, stats-get, status,
params and a notify handshake. Anything else is ENOSYS and logged.

    flux python -m fluxion_graphdb --db graphdb/graph.lbug
    flux module load sched-fluxion-qmanager queue-policy=easy
"""

from __future__ import annotations

import errno

from .resource import SERVICE, Resource


def serve(resource: Resource, log=print) -> None:
    """Register the service on this Flux instance and answer until the reactor stops."""
    import flux  # the Flux Python bindings: run under flux python
    from flux.core.handle import ffi

    h = flux.Flux()

    def fail(msg, err: OSError) -> None:
        h.flux_respond_error(msg.handle, err.errno, (err.strerror or "").encode())

    def on_request(h, w, msg, arg):
        topic = msg.topic[len(SERVICE) + 1:]
        if topic == "disconnect":
            return
        payload = msg.payload if msg.payload_str else {}
        try:
            if topic == "match":
                h.respond(msg, resource.match(payload["cmd"], int(payload["jobid"]), payload["jobspec"]))
            elif topic == "match_multi":
                for job in payload["jobs"]:
                    try:
                        h.respond(msg, resource.match(payload["cmd"], int(job["jobid"]), job["jobspec"]))
                    except OSError as e:
                        if e.errno not in (errno.EBUSY, errno.ENODEV):
                            log(f"match_multi: job {job['jobid']}: {e.strerror}")
                        h.flux_respond_error(msg.handle, e.errno, f"jobid={job['jobid']}".encode())
                        return
                h.flux_respond_error(msg.handle, errno.ENODATA, ffi.NULL)
            elif topic == "cancel":
                resource.cancel(int(payload["jobid"]))
                h.respond(msg, {})
            elif topic == "partial-cancel":
                resource.cancel(int(payload["jobid"]))
                h.respond(msg, {"full-removal": 1})
            elif topic == "update":
                h.respond(msg, resource.update(int(payload["jobid"]), payload["R"]))
            elif topic == "info":
                h.respond(msg, resource.info(int(payload["jobid"])))
            elif topic == "stats-get":
                h.respond(msg, resource.stats())
            elif topic == "stats-clear":
                resource.matches.clear()
                h.respond(msg, {})
            elif topic == "notify":
                h.respond(msg, {"resources": {}})  # qmanager's handshake; it reads only the return code
            elif topic == "status":
                h.respond(msg, {"all": None, "down": None, "allocated": None})
            elif topic == "params":
                h.respond(msg, {"params": {"backend": "graphdb", "rank": resource.by}})
            else:
                log(f"{SERVICE}.{topic}: not implemented (payload keys {sorted(payload)})")
                raise OSError(errno.ENOSYS, f"{topic} is not implemented by the graph database service")
        except OSError as e:
            fail(msg, e)
        except (KeyError, TypeError, ValueError) as e:
            fail(msg, OSError(errno.EPROTO, f"bad request: {e}"))

    h.service_register(SERVICE).get()
    w = h.msg_watcher_create(on_request, flux.constants.FLUX_MSGTYPE_REQUEST, SERVICE + ".*")
    w.start()
    log(f"{SERVICE}: serving from {getattr(resource.db, 'path', 'the database')} with {len(resource.jobs)} jobs on record")
    try:
        h.reactor_run()
    finally:
        h.service_unregister(SERVICE).get()
