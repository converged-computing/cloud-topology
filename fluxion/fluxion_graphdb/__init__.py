"""Fluxion's resource service, sched-fluxion-resource, answered from the graph database."""

from .resource import SERVICE, Resource, rv1
from .service import serve

__all__ = ["SERVICE", "Resource", "rv1", "serve"]
