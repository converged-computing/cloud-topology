module github.com/converged-computing/google-cloud-topology/examples/fluxion-go

go 1.26.0

require github.com/flux-framework/flux-sched/resource/reapi/bindings/go v0.0.0

// The bindings build against a local flux-sched checkout (set up by the devcontainer).
replace github.com/flux-framework/flux-sched/resource/reapi/bindings/go => /opt/flux-sched/resource/reapi/bindings/go
