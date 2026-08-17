FROM debian:13.6-slim@sha256:3a39a0592364683e6bab97937b72cad5a8fa6dcbbee90edb3bb48c7f8e94f258

ARG DOMUS_UID=1000
ARG DOMUS_GID=1000

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      bash \
      ca-certificates \
      coreutils \
      curl \
      ffmpeg \
      file \
      findutils \
      git \
      grep \
      imagemagick \
      jq \
      less \
      nano \
      openssh-client \
      passwd \
      poppler-utils \
      python3 \
      rsync \
      sed \
      tar \
      tzdata \
      unzip \
      util-linux \
      wget \
      zip \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid "${DOMUS_GID}" domus \
    && useradd --uid "${DOMUS_UID}" --gid "${DOMUS_GID}" \
      --home-dir /workspace --shell /bin/bash --no-create-home domus \
    && install -d -o "${DOMUS_UID}" -g "${DOMUS_GID}" -m 0755 /workspace

COPY --chmod=0755 domus-preview /usr/local/bin/domus-preview
COPY --chmod=0755 domus-transcode /usr/local/bin/domus-transcode

ENV LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    SHELL=/bin/bash \
    TERM=xterm-256color

USER ${DOMUS_UID}:${DOMUS_GID}
WORKDIR /workspace

CMD ["/bin/sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done"]
