# A throwaway "server" for testing the bring-your-own path locally: Ubuntu 24.04 with systemd as
# PID 1, so the install one-liner exercises the real systemd unit. Not for production.
#
#   docker build -t cloudy-dev-target deploy/dev-target
#   docker run -d --name cloudy-target --privileged --cgroupns=host \
#     -v /sys/fs/cgroup:/sys/fs/cgroup:rw --add-host=host.docker.internal:host-gateway cloudy-dev-target
#   docker exec cloudy-target sh -c 'curl -fsSL <install URL from the UI> | sh'
#
# The API must be started with CLOUDY_AGENT_URL=http://host.docker.internal:8710 so the install
# script and the agent reach it from inside the container.
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv curl ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && systemctl mask systemd-logind.service getty.target console-getty.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
