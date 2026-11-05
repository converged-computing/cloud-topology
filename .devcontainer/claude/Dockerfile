FROM fluxrm/flux-core:bookworm

LABEL maintainer="Vanessasaurus <@vsoch>"

# Match the default user id for a single system so we aren't root
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000
ARG CLAUDE_CODE_VERSION=latest
ARG GO_VERSION=1.26.10
# Fluxion (flux-sched) tag to build; resource-query runs the examples and reapi_cli backs the Go bindings.
ARG FLUX_SCHED_VERSION=v0.56.0
ENV USERNAME=${USERNAME}
ENV USER_UID=${USER_UID}
ENV USER_GID=${USER_GID}
USER root

# Install extra buildrequires for flux-sched:
RUN sudo apt-get update
RUN sudo apt-get -qq install -y --no-install-recommends \
        libboost-graph-dev \
        libboost-system-dev \
        libboost-filesystem-dev \
        libboost-regex-dev \
        python3-yaml \
        libyaml-cpp-dev \
        libedit-dev \
        ninja-build \
        curl \
        nodejs \
        npm \
        iptables \
        ipset \
        iproute2 \
        dnsutils \
        aggregate

# Assuming installing to /usr/local
ENV LD_LIBRARY_PATH=/usr/lib:/usr/local/lib

RUN curl -s -L https://github.com/Kitware/CMake/releases/download/v3.26.4/cmake-3.26.4-linux-$(uname -m).sh > cmake.sh ;\
    sudo bash cmake.sh --prefix=/usr/local --skip-license ;\
    rm cmake.sh

# extra interactive utilities
# extra interactive utilities
RUN apt-get update \
 && apt-get -qq install -y --no-install-recommends \
    fd-find \
    less \
    bats \
    ripgrep \
    sqlite3 \
    # flux-accounting specific dependencies. libsqlite3-dev is already in the base
    python3-pip && \
    rm -rf /var/lib/apt/lists/* && \
    python3 -m pip install --break-system-packages 'black~=26.1'

# Build Fluxion: resource-query for the CLI examples, libreapi_cli for the Go bindings.
RUN git clone --depth 1 --branch ${FLUX_SCHED_VERSION} https://github.com/flux-framework/flux-sched /opt/flux-sched \
 && cmake -S /opt/flux-sched -B /opt/flux-sched/build -G Ninja -DCMAKE_BUILD_TYPE=Release \
 && ninja -C /opt/flux-sched/build resource-query reapi_cli \
 && ln -s /opt/flux-sched/build/resource/utilities/resource-query /usr/local/bin/resource-query

# LadybugDB shell, for Cypher queries over the resource graphs (make graphdb).
ARG LBUG_VERSION=v0.22.1
RUN arch=$(uname -m | sed 's/arm64/aarch64/') \
 && curl -sL https://github.com/LadybugDB/ladybug/releases/download/${LBUG_VERSION}/lbug_cli-linux-${arch}.tar.gz | tar xz -C /tmp \
 && install -m 755 $(find /tmp -maxdepth 2 -type f -name lbug) /usr/local/bin/lbug && rm -rf /tmp/lbug*

# Install Claude
RUN npm install -g @anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}
RUN sudo npm i -g opencode-ai

RUN arch=$(dpkg --print-architecture) \
 && curl -sSL https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz | tar -C /usr/local -xz

RUN case $(dpkg --print-architecture) in amd64) a=x86_64 ;; arm64) a=arm ;; esac \
 && curl -sSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-${a}.tar.gz | tar -C /opt -xz \
 && /opt/google-cloud-sdk/install.sh --quiet --usage-reporting false --path-update false

ENV PATH=$PATH:/usr/local/go/bin:/opt/google-cloud-sdk/bin:/home/${USERNAME}/go/bin
# cgo settings for google/examples/fluxion-go (Fluxion's Go bindings).
ENV CGO_CFLAGS="-I/opt/flux-sched"
ENV CGO_LDFLAGS="-L/opt/flux-sched/build/resource/reapi/bindings -lreapi_cli -lflux-idset -lstdc++ -ljansson -lhwloc -lboost_system -lflux-hostlist -lboost_graph -lyaml-cpp -Wl,-rpath,/opt/flux-sched/build/resource/reapi/bindings"

# Add the group and user that match our ids
RUN groupadd -g ${USER_GID} ${USERNAME} && \
    adduser --disabled-password --uid ${USER_UID} --gid ${USER_GID} --gecos "" ${USERNAME} && \
    echo "${USERNAME} ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers
USER $USERNAME

# Copy and set up firewall script
RUN mkdir -p /home/vscode/.claude/
COPY .devcontainer/claude/init-firewall.sh /usr/local/bin/
COPY .devcontainer/claude/settings.json /home/vscode/.claude/settings.json
USER root
RUN chmod +x /usr/local/bin/init-firewall.sh && \
  echo "${USERNAME} ALL=(root) NOPASSWD: /usr/local/bin/init-firewall.sh" > /etc/sudoers.d/${USERNAME}-firewall && \
  chmod 0440 /etc/sudoers.d/${USERNAME}-firewall
USER $USERNAME
