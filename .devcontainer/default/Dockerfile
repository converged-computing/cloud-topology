# Development environment: Fluxion (resource-query and Go bindings), Go, hwloc, gcloud and
# topology-agent.
# Adapted from flux-sched's own .devcontainer.
FROM fluxrm/flux-core:bookworm-v0.88.0

ARG FLUX_SCHED_VERSION=v0.56.0
ARG GO_VERSION=1.26.10
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000
# The agent libraries: a pip source each (git URL or path inside the build context) and the
# behalf backend extra. Set a source to "" to skip it (then: make install-agents).
ARG TOPOLOGY_AGENT_SOURCE=git+https://github.com/converged-computing/topology-agent@main
ARG TOPOLOGY_AGENT_EXTRAS=claude
ARG INGEST_AGENT_SOURCE=git+https://github.com/converged-computing/ingest-agent@main
ARG INGEST_AGENT_EXTRAS=claude
USER root

RUN apt-get update && apt-get -qq install -y --no-install-recommends \
        libboost-graph-dev \
        libboost-system-dev \
        libboost-filesystem-dev \
        libboost-regex-dev \
        libyaml-cpp-dev \
        libedit-dev \
        libhwloc-dev \
        libjansson-dev \
        uuid-dev \
        python3-yaml \
        python3-venv \
        cmake \
        ninja-build \
        git \
        curl \
        ca-certificates \
        hwloc-nox \
        jq \
        less \
        sudo \
 && rm -rf /var/lib/apt/lists/*

# Build Fluxion: resource-query for the CLI examples, libreapi_cli for the Go bindings.
RUN git clone --depth 1 --branch ${FLUX_SCHED_VERSION} https://github.com/flux-framework/flux-sched /opt/flux-sched \
 && cmake -S /opt/flux-sched -B /opt/flux-sched/build -G Ninja -DCMAKE_BUILD_TYPE=Release \
 && ninja -C /opt/flux-sched/build resource-query reapi_cli \
 && ln -s /opt/flux-sched/build/resource/utilities/resource-query /usr/local/bin/resource-query

RUN arch=$(dpkg --print-architecture) \
 && curl -sSL https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz | tar -C /usr/local -xz

RUN case $(dpkg --print-architecture) in amd64) a=x86_64 ;; arm64) a=arm ;; esac \
 && curl -sSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-${a}.tar.gz | tar -C /opt -xz \
 && /opt/google-cloud-sdk/install.sh --quiet --usage-reporting false --path-update false

# AWS CLI v2, for aws/ (credentials come from the host's ~/.aws or the environment).
RUN case $(dpkg --print-architecture) in amd64) a=x86_64 ;; arm64) a=aarch64 ;; esac \
 && curl -sSL -o /tmp/awscliv2.zip https://awscli.amazonaws.com/awscli-exe-linux-${a}.zip \
 && unzip -q /tmp/awscliv2.zip -d /tmp && /tmp/aws/install && rm -rf /tmp/aws /tmp/awscliv2.zip

ENV PATH=$PATH:/usr/local/go/bin:/opt/google-cloud-sdk/bin:/home/${USERNAME}/go/bin
# cgo settings for examples/fluxion-go (Fluxion's Go bindings).
ENV CGO_CFLAGS="-I/opt/flux-sched"
ENV CGO_LDFLAGS="-L/opt/flux-sched/build/resource/reapi/bindings -lreapi_cli -lflux-idset -lstdc++ -ljansson -lhwloc -lboost_system -lflux-hostlist -lboost_graph -lyaml-cpp -Wl,-rpath,/opt/flux-sched/build/resource/reapi/bindings"

RUN groupadd -g ${USER_GID} ${USERNAME} \
 && adduser --disabled-password --uid ${USER_UID} --gid ${USER_GID} --gecos "" ${USERNAME} \
 && echo "${USERNAME} ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/${USERNAME}

# Both agents in one virtual environment on the PATH.
RUN python3 -m venv /opt/agents \
 && if [ -n "${TOPOLOGY_AGENT_SOURCE}" ]; then /opt/agents/bin/pip install -q "topology-agent[${TOPOLOGY_AGENT_EXTRAS}] @ ${TOPOLOGY_AGENT_SOURCE}"; fi \
 && if [ -n "${INGEST_AGENT_SOURCE}" ]; then /opt/agents/bin/pip install -q "ingest-agent[${INGEST_AGENT_EXTRAS}] @ ${INGEST_AGENT_SOURCE}"; fi \
 && chown -R ${USER_UID}:${USER_GID} /opt/agents
ENV PATH=/opt/agents/bin:$PATH
USER ${USERNAME}
