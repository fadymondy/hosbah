# A throwaway "server" for testing the bring-your-own path locally: Ubuntu 24.04 with systemd as
# PID 1, so the install one-liner exercises the real systemd unit. Not for production.
#
#   docker build -t hosbah-dev-target deploy/dev-target
#   docker run -d --name hosbah-target --privileged --cgroupns=host \
#     -v /sys/fs/cgroup:/sys/fs/cgroup:rw -v hosbah-target-docker:/var/lib/docker \
#     -v hosbah-target-containerd:/var/lib/containerd \
#     --add-host=host.docker.internal:host-gateway hosbah-dev-target
#
# (A target created before the rename is named cloudy-target, with cloudy-target-* volumes: keep
# using it under those names; its agent moves itself to hosbah-agent on its next update.)
#
# The two volumes matter for P2 builds: the inner Docker's storage must NOT sit on the outer
# container's overlayfs, or every build fails at its first step with
#   mount source: "overlay" ... err: invalid argument
# because BuildKit cannot mount overlay on overlay. A volume is ext4 from the Docker Desktop VM.
# On a container that is already enrolled, the same is achieved without recreating it, with a loop
# file: truncate -s 60G /srv/dockerfs.img; mkfs.ext4 -q /srv/dockerfs.img;
# mount -o loop /srv/dockerfs.img /srv/dockerfs; then bind /var/lib/docker and /var/lib/containerd
# into it (and add all three to /etc/fstab).
#   docker exec hosbah-target sh -c 'curl -fsSL <install URL from the UI> | sh'
#
# The API must be started with HOSBAH_AGENT_URL=http://host.docker.internal:8710 so the install
# script and the agent reach it from inside the container.
FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive container=docker
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv curl ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && systemctl mask systemd-logind.service getty.target console-getty.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
